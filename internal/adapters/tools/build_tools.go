package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// Ensure path/filepath is used.
var _ = filepath.Join

type RepoStaticAnalysisHandler struct {
	runner app.CommandRunner
}

type RepoPackageHandler struct {
	runner app.CommandRunner
}

func NewRepoStaticAnalysisHandler(runner app.CommandRunner) *RepoStaticAnalysisHandler {
	return &RepoStaticAnalysisHandler{runner: runner}
}

func NewRepoPackageHandler(runner app.CommandRunner) *RepoPackageHandler {
	return &RepoPackageHandler{runner: runner}
}

func (h *RepoStaticAnalysisHandler) Name() string {
	return "repo.static_analysis"
}

func (h *RepoStaticAnalysisHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid repo.static_analysis args",
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported static analysis toolchain found",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, commandErr := staticAnalysisCommandForProject(
		session.WorkspacePath,
		detected,
		sanitizeTarget(request.Target),
	)
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
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: commandResult.Output}},
		Output: map[string]any{
			"project_type": detected.Name,
			"command":      append([]string{command}, commandArgs...),
			"exit_code":    commandResult.ExitCode,
			"diagnostics":  extractDiagnostics(commandResult.Output, 80),
			"output":       commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweArtifactStaticAnalysisOutput,
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *RepoPackageHandler) Name() string {
	return "repo.package"
}

func (h *RepoPackageHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid repo.package args",
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported packaging toolchain found",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, artifactPath, ensureDist, commandErr := packageCommandForProject(
		session.WorkspacePath,
		detected,
		sanitizeTarget(request.Target),
	)
	if commandErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   commandErr.Error(),
			Retryable: false,
		}
	}
	if ensureDist {
		if _, mkdirErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "mkdir",
			Args:     []string{"-p", sweWorkspaceDist},
			MaxBytes: 16 * 1024,
		}); mkdirErr != nil {
			return app.ToolRunResult{}, toToolError(mkdirErr, "failed to create "+sweWorkspaceDist)
		}
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  command,
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})

	if detected.Name == sweEcosystemNode {
		if packed := detectNodePackageArtifact(commandResult.Output); packed != "" {
			artifactPath = packed
		}
	}

	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: commandResult.Output}},
		Output: map[string]any{
			"project_type":  detected.Name,
			"command":       append([]string{command}, commandArgs...),
			"artifact_path": artifactPath,
			"exit_code":     commandResult.ExitCode,
			"output":        commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweArtifactPackageOutput,
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func staticAnalysisCommandForProject(workspacePath string, detected projectType, target string) (string, []string, error) {
	switch detected.Name {
	case sweEcosystemGo:
		return "go", []string{"vet", targetOrDefault(target, "./...")}, nil
	case sweEcosystemRust:
		return "cargo", []string{"clippy", "--all-targets", "--all-features", "--", "-D", "warnings"}, nil
	case sweEcosystemNode:
		args := []string{"run", "lint", "--if-present"}
		if strings.TrimSpace(target) != "" {
			args = append(args, "--", target)
		}
		return "npm", args, nil
	case sweEcosystemPython:
		pythonExecutable := resolvePythonExecutable(workspacePath)
		return pythonExecutable, []string{"-m", "compileall", targetOrDefault(target, ".")}, nil
	case sweEcosystemJava:
		if detected.Flavor == "gradle" {
			return "gradle", []string{"check", "-x", "test"}, nil
		}
		return "mvn", []string{"-q", "-DskipTests", "verify"}, nil
	case sweEcosystemC:
		source, sourceErr := resolveCSourceForBuild(workspacePath, target)
		if sourceErr != nil {
			return "", nil, sourceErr
		}
		return "cc", []string{"-std=c11", "-fsyntax-only", source}, nil
	default:
		return "", nil, os.ErrNotExist
	}
}

func packageCommandForProject(workspacePath string, detected projectType, target string) (string, []string, string, bool, error) {
	switch detected.Name {
	case sweEcosystemGo:
		args := []string{"build", "-o", sweWorkspaceDist + "/app"}
		args = append(args, targetOrDefault(target, "."))
		return "go", args, sweWorkspaceDist + "/app", true, nil
	case sweEcosystemRust:
		args := []string{"build", "--release"}
		if strings.TrimSpace(target) != "" {
			args = append(args, "--package", target)
		}
		return "cargo", args, "target/release", false, nil
	case sweEcosystemNode:
		return "npm", []string{"pack", "--silent"}, "", false, nil
	case sweEcosystemPython:
		pythonExecutable := resolvePythonExecutable(workspacePath)
		return pythonExecutable, []string{"-m", "pip", "wheel", ".", "-w", sweWorkspaceDist}, sweWorkspaceDist, true, nil
	case sweEcosystemJava:
		if detected.Flavor == "gradle" {
			return "gradle", []string{"assemble"}, "build/libs", false, nil
		}
		return "mvn", []string{"-q", "-DskipTests", "package"}, "target", false, nil
	case sweEcosystemC:
		source, sourceErr := resolveCSourceForBuild(workspacePath, target)
		if sourceErr != nil {
			return "", nil, "", false, sourceErr
		}
		return "cc", []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-o", sweWorkspaceDist + "/c-app", source}, sweWorkspaceDist + "/c-app", true, nil
	default:
		return "", nil, "", false, os.ErrNotExist
	}
}

func detectNodePackageArtifact(output string) string {
	lines := splitOutputLines(output)
	for _, line := range lines {
		if strings.HasSuffix(line, ".tgz") && !strings.Contains(line, " ") {
			return line
		}
	}
	return ""
}
