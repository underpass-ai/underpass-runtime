package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

var (
	coverageTotalPattern = regexp.MustCompile(`total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%`)
	coverageLinePattern  = regexp.MustCompile(`coverage:\s+([0-9]+(?:\.[0-9]+)?)%`)
)

type toolchainInfo struct {
	Language        string
	BuildSystem     string
	TestFramework   string
	Containerizable bool
}

type RepoDetectToolchainHandler struct {
	runner app.CommandRunner
}

type RepoValidateHandler struct {
	runner app.CommandRunner
}

type RepoTestHandler struct {
	runner app.CommandRunner
}

type GoModTidyHandler struct {
	runner app.CommandRunner
}

type GoGenerateHandler struct {
	runner app.CommandRunner
}

type GoBuildHandler struct {
	runner app.CommandRunner
}

type GoTestHandler struct {
	runner app.CommandRunner
}

func NewRepoDetectToolchainHandler(runner app.CommandRunner) *RepoDetectToolchainHandler {
	return &RepoDetectToolchainHandler{runner: runner}
}

func NewRepoValidateHandler(runner app.CommandRunner) *RepoValidateHandler {
	return &RepoValidateHandler{runner: runner}
}

func NewRepoTestHandler(runner app.CommandRunner) *RepoTestHandler {
	return &RepoTestHandler{runner: runner}
}

func NewGoModTidyHandler(runner app.CommandRunner) *GoModTidyHandler {
	return &GoModTidyHandler{runner: runner}
}

func NewGoGenerateHandler(runner app.CommandRunner) *GoGenerateHandler {
	return &GoGenerateHandler{runner: runner}
}

func NewGoBuildHandler(runner app.CommandRunner) *GoBuildHandler {
	return &GoBuildHandler{runner: runner}
}

func NewGoTestHandler(runner app.CommandRunner) *GoTestHandler {
	return &GoTestHandler{runner: runner}
}

func (h *RepoDetectToolchainHandler) Name() string {
	return "repo.detect_toolchain"
}

func (h *RepoDetectToolchainHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	if len(args) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(args, &payload); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.detect_toolchain args",
				Retryable: false,
			}
		}
	}

	detected, err := detectProjectTypeForSession(ctx, ensureRunner(h.runner), session)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   err.Error(),
			Retryable: false,
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		detected = projectType{Name: "unknown"}
	}

	toolchain := mapProjectTypeToToolchain(detected)
	return app.ToolRunResult{
		Output: map[string]any{
			"language":        toolchain.Language,
			"build_system":    toolchain.BuildSystem,
			"test_framework":  toolchain.TestFramework,
			"containerizable": toolchain.Containerizable,
		},
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: fsKeyStdout,
			Message: fmt.Sprintf("detected toolchain: %s", toolchain.Language),
		}},
	}, nil
}

func (h *RepoValidateHandler) Name() string {
	return "repo.validate"
}

func (h *RepoValidateHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.validate args",
				Retryable: false,
			}
		}
	}

	runner := ensureRunner(h.runner)
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported toolchain found",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, commandErr := validateCommandForProject(session.WorkspacePath, detected, request.Target)
	if commandErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   commandErr.Error(),
			Retryable: false,
		}
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  command,
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})

	toolchain := mapProjectTypeToToolchain(detected)
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output}},
		Output: map[string]any{
			"language":       toolchain.Language,
			"build_system":   toolchain.BuildSystem,
			"test_framework": toolchain.TestFramework,
			"command":        append([]string{command}, commandArgs...),
			"exit_code":      commandResult.ExitCode,
			"diagnostics":    extractDiagnostics(commandResult.Output, 50),
			"output":         commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "validate-output.txt",
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *RepoTestHandler) Name() string {
	return "repo.test"
}

func (h *RepoTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	delegate := RepoRunTestsHandler{runner: h.runner}
	return delegate.Invoke(ctx, session, args)
}

func (h *GoModTidyHandler) Name() string {
	return "go.mod.tidy"
}

func (h *GoModTidyHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Check bool `json:"check"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid go.mod.tidy args",
				Retryable: false,
			}
		}
	}

	commandArgs := []string{"mod", "tidy", "-v"}
	if request.Check {
		commandArgs = []string{"mod", "tidy", "-diff"}
	}

	commandResult, runErr := ensureRunner(h.runner).Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     commandArgs,
		MaxBytes: 1024 * 1024,
	})
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output}},
		Output: map[string]any{
			"exit_code":            commandResult.ExitCode,
			"compiled_binary_path": "",
			"coverage_percent":     0.0,
			"diagnostics":          extractDiagnostics(commandResult.Output, 50),
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "go-mod-tidy-output.txt",
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *GoGenerateHandler) Name() string {
	return "go.generate"
}

func (h *GoGenerateHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid go.generate args",
				Retryable: false,
			}
		}
	}

	target := sanitizeTarget(request.Target)
	if target == "" {
		target = "./..."
	}

	commandResult, runErr := ensureRunner(h.runner).Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     []string{"generate", target},
		MaxBytes: 1024 * 1024,
	})
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output}},
		Output: map[string]any{
			"exit_code":            commandResult.ExitCode,
			"compiled_binary_path": "",
			"coverage_percent":     0.0,
			"diagnostics":          extractDiagnostics(commandResult.Output, 50),
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "go-generate-output.txt",
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *GoBuildHandler) Name() string {
	return "go.build"
}

func (h *GoBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target     string `json:"target"`
		Ldflags    string `json:"ldflags"`
		OutputName string `json:"output_name"`
		Race       bool   `json:"race"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid go.build args",
				Retryable: false,
			}
		}
	}

	safeLdflags, ldflagsErr := sanitizeGoLdflags(request.Ldflags)
	if ldflagsErr != nil {
		return app.ToolRunResult{}, ldflagsErr
	}
	outputName, outputErr := sanitizeOutputName(request.OutputName)
	if outputErr != nil {
		return app.ToolRunResult{}, outputErr
	}

	target := sanitizeTarget(request.Target)
	if target == "" {
		target = "."
	}

	commandArgs := []string{"build"}
	if request.Race {
		commandArgs = append(commandArgs, "-race")
	}
	if safeLdflags != "" {
		commandArgs = append(commandArgs, "-ldflags", safeLdflags)
	}

	compiledBinaryPath := ""
	if outputName != "" {
		commandArgs = append(commandArgs, "-o", outputName)
		compiledBinaryPath = filepath.ToSlash(outputName)
	}
	commandArgs = append(commandArgs, target)

	commandResult, runErr := ensureRunner(h.runner).Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output}},
		Output: map[string]any{
			"exit_code":            commandResult.ExitCode,
			"compiled_binary_path": compiledBinaryPath,
			"coverage_percent":     0.0,
			"diagnostics":          extractDiagnostics(commandResult.Output, 50),
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "go-build-output.txt",
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *GoTestHandler) Name() string {
	return "go.test"
}

func (h *GoTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Package    string `json:"package"`
		Coverage   bool   `json:"coverage"`
		RunPattern string `json:"run_pattern"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid go.test args",
				Retryable: false,
			}
		}
	}

	target := sanitizeTarget(request.Package)
	if target == "" {
		target = "./..."
	}
	runPattern := strings.TrimSpace(request.RunPattern)
	if strings.Contains(runPattern, "\x00") || len(runPattern) > 512 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid run_pattern",
			Retryable: false,
		}
	}

	commandArgs := []string{"test", target}
	coverFile := ""
	if runPattern != "" {
		commandArgs = append(commandArgs, "-run", runPattern)
	}
	if request.Coverage {
		coverFile = ".workspace.cover.out"
		commandArgs = append(commandArgs, "-coverprofile", coverFile, "-covermode=atomic")
	}

	runner := ensureRunner(h.runner)
	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})

	coveragePercent := calculateGoTestCoverage(ctx, runner, session, request.Coverage, commandResult, coverFile)

	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output}},
		Output: map[string]any{
			"exit_code":            commandResult.ExitCode,
			"compiled_binary_path": "",
			"coverage_percent":     coveragePercent,
			"diagnostics":          extractDiagnostics(commandResult.Output, 80),
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "go-test-output.txt",
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func calculateGoTestCoverage(ctx context.Context, runner app.CommandRunner, session domain.Session, doCoverage bool, commandResult app.CommandResult, coverFile string) float64 {
	if !doCoverage || commandResult.ExitCode != 0 {
		return 0.0
	}
	coveragePercent := 0.0
	if parsed := parseCoveragePercent(commandResult.Output); parsed != nil {
		coveragePercent = *parsed
	}
	if coverFile == "" {
		return coveragePercent
	}
	coverResult, coverErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     []string{"tool", "cover", "-func=" + coverFile},
		MaxBytes: 256 * 1024,
	})
	if coverErr == nil {
		if parsed := parseCoveragePercent(coverResult.Output); parsed != nil {
			coveragePercent = *parsed
		}
	}
	_ = os.Remove(filepath.Join(session.WorkspacePath, coverFile))
	return coveragePercent
}

func ensureRunner(runner app.CommandRunner) app.CommandRunner {
	if runner != nil {
		return runner
	}
	return NewLocalCommandRunner()
}

func mapProjectTypeToToolchain(detected projectType) toolchainInfo {
	switch detected.Name {
	case "go":
		return toolchainInfo{
			Language:        "go",
			BuildSystem:     "go-mod",
			TestFramework:   "testing",
			Containerizable: true,
		}
	case "node":
		testFramework := "npm-test"
		buildSystem := "npm"
		if detected.Flavor == "typescript" {
			testFramework = "vitest/jest"
			buildSystem = "npm-ts"
		}
		return toolchainInfo{
			Language:        "node",
			BuildSystem:     buildSystem,
			TestFramework:   testFramework,
			Containerizable: true,
		}
	case "python":
		return toolchainInfo{
			Language:        "python",
			BuildSystem:     "pip",
			TestFramework:   "pytest",
			Containerizable: true,
		}
	case "java":
		buildSystem := "maven"
		if detected.Flavor == "gradle" {
			buildSystem = "gradle"
		}
		return toolchainInfo{
			Language:        "java",
			BuildSystem:     buildSystem,
			TestFramework:   "junit",
			Containerizable: true,
		}
	case "rust":
		return toolchainInfo{
			Language:        "rust",
			BuildSystem:     "cargo",
			TestFramework:   "cargo-test",
			Containerizable: true,
		}
	case "c":
		return toolchainInfo{
			Language:        "c",
			BuildSystem:     "cc",
			TestFramework:   "native",
			Containerizable: true,
		}
	default:
		return toolchainInfo{
			Language:        "unknown",
			BuildSystem:     "unknown",
			TestFramework:   "unknown",
			Containerizable: false,
		}
	}
}

func validateCommandForProject(workspacePath string, detected projectType, target string) (string, []string, error) {
	sanitizedTarget := sanitizeTarget(target)
	switch detected.Name {
	case "go":
		if sanitizedTarget == "" {
			sanitizedTarget = "./..."
		}
		return "go", []string{"test", sanitizedTarget, "-run", "^$"}, nil
	case "python":
		if sanitizedTarget == "" {
			sanitizedTarget = "."
		}
		return "python", []string{"-m", "compileall", sanitizedTarget}, nil
	case "node":
		return "npm", []string{"run", "lint", "--if-present"}, nil
	case "java":
		if detected.Flavor == "gradle" {
			return "gradle", []string{"classes"}, nil
		}
		return "mvn", []string{"-q", "-DskipTests", "compile"}, nil
	case "rust":
		return "cargo", []string{"check"}, nil
	case "c":
		source, sourceErr := resolveCSourceForBuild(workspacePath, sanitizedTarget)
		if sourceErr != nil {
			return "", nil, sourceErr
		}
		return "cc", []string{"-std=c11", "-fsyntax-only", source}, nil
	default:
		return "", nil, os.ErrNotExist
	}
}

func sanitizeOutputName(outputName string) (string, *domain.Error) {
	trimmed := strings.TrimSpace(outputName)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > 120 {
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "output_name exceeds 120 characters",
			Retryable: false,
		}
	}
	if strings.Contains(trimmed, "\x00") || strings.Contains(trimmed, "..") {
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "output_name contains invalid characters",
			Retryable: false,
		}
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "output_name must be a plain file name",
			Retryable: false,
		}
	}
	return trimmed, nil
}

func sanitizeGoLdflags(ldflags string) (string, *domain.Error) {
	trimmed := strings.TrimSpace(ldflags)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > 120 {
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "ldflags exceeds 120 characters",
			Retryable: false,
		}
	}
	if strings.ContainsAny(trimmed, "&;|`$<>\n\r\t") {
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "ldflags contains unsupported characters",
			Retryable: false,
		}
	}
	for _, token := range strings.Fields(trimmed) {
		if token == "-s" || token == "-w" {
			continue
		}
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "ldflags only allows -s and -w",
			Retryable: false,
		}
	}
	return strings.Join(strings.Fields(trimmed), " "), nil
}

func parseCoveragePercent(output string) *float64 {
	text := strings.TrimSpace(output)
	if text == "" {
		return nil
	}

	if matches := coverageTotalPattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		value := matches[len(matches)-1][1]
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return &parsed
		}
	}
	if matches := coverageLinePattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		value := matches[len(matches)-1][1]
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func extractDiagnostics(output string, max int) []string {
	if max <= 0 {
		max = 20
	}

	lines := splitOutputLines(output)
	diagnostics := make([]string, 0, minInt(len(lines), max))
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "warning") ||
			strings.Contains(lower, "fail") ||
			strings.Contains(lower, "undefined") {
			diagnostics = append(diagnostics, line)
		}
		if len(diagnostics) >= max {
			return diagnostics
		}
	}
	if len(diagnostics) > 0 {
		return diagnostics
	}

	for _, line := range lines {
		diagnostics = append(diagnostics, line)
		if len(diagnostics) >= max {
			break
		}
	}
	return diagnostics
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
