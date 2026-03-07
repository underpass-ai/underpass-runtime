package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	repoTypeUnknown = "unknown"
	repoTypePython  = "python"
	repoTypeNode    = "node"
	repoTypeJava    = "java"
	repoTypeRust    = "rust"
	repoKeyBuild    = "build"
	repoBuildMaven  = "maven"
	repoFlagPackage = "--package"
	repoBuildGradle = "gradle"
	repoBuildCargo  = "cargo"
)

type RepoDetectProjectTypeHandler struct {
	runner app.CommandRunner
}

type RepoBuildHandler struct {
	runner app.CommandRunner
}

type RepoRunTestsHandler struct {
	runner app.CommandRunner
}

type projectType struct {
	Name   string
	Flavor string
}

func NewRepoDetectProjectTypeHandler(runner app.CommandRunner) *RepoDetectProjectTypeHandler {
	return &RepoDetectProjectTypeHandler{runner: runner}
}

func NewRepoBuildHandler(runner app.CommandRunner) *RepoBuildHandler {
	return &RepoBuildHandler{runner: runner}
}

func NewRepoRunTestsHandler(runner app.CommandRunner) *RepoRunTestsHandler {
	return &RepoRunTestsHandler{runner: runner}
}

func (h *RepoDetectProjectTypeHandler) Name() string {
	return "repo.detect_project_type"
}

func (h *RepoDetectProjectTypeHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	if len(args) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(args, &payload); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.detect_project_type args",
				Retryable: false,
			}
		}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	detected, err := detectProjectTypeForSession(ctx, runner, session)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   err.Error(),
			Retryable: false,
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		detected = projectType{Name: repoTypeUnknown}
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"project_type": detected.Name,
			"flavor":       detected.Flavor,
		},
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: fmt.Sprintf("detected project type: %s", detected.Name),
		}},
	}, nil
}

func (h *RepoBuildHandler) Name() string {
	return "repo.build"
}

func (h *RepoBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target    string   `json:"target"`
		ExtraArgs []string `json:"extra_args"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.build args",
				Retryable: false,
			}
		}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported build tool found (expected go.mod, package.json, pyproject.toml/pytest.ini, pom.xml, or build.gradle)",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	safeExtraArgs, extraArgsErr := filterRepoExtraArgs(detected, request.ExtraArgs, repoKeyBuild)
	if extraArgsErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   extraArgsErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, commandErr := buildCommandForProject(session.WorkspacePath, detected, request.Target, safeExtraArgs)
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
			"output":       commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "build-output.txt",
			ContentType: "text/plain",
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *RepoRunTestsHandler) Name() string {
	return "repo.run_tests"
}

func (h *RepoRunTestsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target    string   `json:"target"`
		ExtraArgs []string `json:"extra_args"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid repo.run_tests args",
				Retryable: false,
			}
		}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported test runner found (expected go.mod, pytest.ini/pyproject.toml, package.json, pom.xml, or build.gradle)",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	safeExtraArgs, extraArgsErr := filterRepoExtraArgs(detected, request.ExtraArgs, "test")
	if extraArgsErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   extraArgsErr.Error(),
			Retryable: false,
		}
	}

	command, commandArgs, commandErr := testCommandForProject(session.WorkspacePath, detected, request.Target, safeExtraArgs)
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
			"output":       commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        "test-output.txt",
			ContentType: "text/plain",
			Data:        []byte(commandResult.Output),
		}},
	}
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}

	return result, nil
}

func detectProjectTypeForSession(ctx context.Context, runner app.CommandRunner, session domain.Session) (projectType, error) {
	if detected, ok := detectProjectTypeFromWorkspace(session.WorkspacePath); ok && session.Runtime.Kind != domain.RuntimeKindKubernetes {
		return detected, nil
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:     session.WorkspacePath,
		Command: "sh",
		Args: []string{
			"-lc",
			"if [ -f go.mod ]; then echo go; " +
				"elif [ -f Cargo.toml ]; then echo rust:cargo; " +
				"elif [ -f package.json ] && [ -f tsconfig.json ]; then echo node:typescript; " +
				"elif [ -f package.json ]; then echo node:npm; " +
				"elif [ -f pyproject.toml ] || [ -f pytest.ini ] || [ -f requirements.txt ]; then echo python:pytest; " +
				"elif [ -f mvnw ] || [ -f pom.xml ]; then echo java:maven; " +
				"elif [ -f gradlew ] || [ -f build.gradle ] || [ -f build.gradle.kts ]; then echo java:gradle; " +
				"elif [ -f CMakeLists.txt ] || find . -maxdepth 3 -name '*.c' -print -quit | grep -q .; then echo c:cc; " +
				"else echo unknown; fi",
		},
		MaxBytes: 4 * 1024,
	})
	if runErr != nil {
		if detected, ok := detectProjectTypeFromWorkspace(session.WorkspacePath); ok {
			return detected, nil
		}
		return projectType{}, runErr
	}

	marker := repoTypeUnknown
	lines := splitOutputLines(commandResult.Output)
	if len(lines) > 0 {
		marker = strings.ToLower(strings.TrimSpace(lines[0]))
	}
	detected := parseProjectMarker(marker)
	if detected.Name == repoTypeUnknown {
		if localDetected, ok := detectProjectTypeFromWorkspace(session.WorkspacePath); ok {
			return localDetected, nil
		}
		return projectType{}, os.ErrNotExist
	}
	return detected, nil
}

func parseProjectMarker(marker string) projectType {
	marker = strings.ToLower(strings.TrimSpace(marker))
	if marker == "" || marker == repoTypeUnknown {
		return projectType{Name: repoTypeUnknown}
	}

	parts := strings.SplitN(marker, ":", 2)
	name := strings.TrimSpace(parts[0])
	flavor := ""
	if len(parts) == 2 {
		flavor = strings.TrimSpace(parts[1])
	}

	switch name {
	case "go", repoTypeNode, repoTypePython, repoTypeJava, repoTypeRust, "c":
		return projectType{Name: name, Flavor: flavor}
	default:
		return projectType{Name: repoTypeUnknown}
	}
}

func detectProjectTypeFromWorkspace(workspacePath string) (projectType, bool) {
	if exists(filepath.Join(workspacePath, "go.mod")) {
		return projectType{Name: "go"}, true
	}
	if exists(filepath.Join(workspacePath, "Cargo.toml")) {
		return projectType{Name: repoTypeRust, Flavor: repoBuildCargo}, true
	}
	if exists(filepath.Join(workspacePath, "package.json")) && exists(filepath.Join(workspacePath, "tsconfig.json")) {
		return projectType{Name: repoTypeNode, Flavor: "typescript"}, true
	}
	if exists(filepath.Join(workspacePath, "package.json")) {
		return projectType{Name: repoTypeNode, Flavor: "npm"}, true
	}
	if exists(filepath.Join(workspacePath, "pytest.ini")) ||
		exists(filepath.Join(workspacePath, "pyproject.toml")) ||
		exists(filepath.Join(workspacePath, "requirements.txt")) {
		return projectType{Name: repoTypePython, Flavor: "pytest"}, true
	}
	if exists(filepath.Join(workspacePath, "mvnw")) || exists(filepath.Join(workspacePath, "pom.xml")) {
		return projectType{Name: repoTypeJava, Flavor: repoBuildMaven}, true
	}
	if exists(filepath.Join(workspacePath, "gradlew")) ||
		exists(filepath.Join(workspacePath, "build.gradle")) ||
		exists(filepath.Join(workspacePath, "build.gradle.kts")) {
		return projectType{Name: repoTypeJava, Flavor: repoBuildGradle}, true
	}
	if exists(filepath.Join(workspacePath, "CMakeLists.txt")) || hasCSourceFiles(workspacePath, true) {
		return projectType{Name: "c", Flavor: "cc"}, true
	}
	return projectType{}, false
}

func detectBuildCommand(workspacePath, target string, extraArgs []string) (string, []string, error) {
	detected, ok := detectProjectTypeFromWorkspace(workspacePath)
	if !ok {
		return "", nil, os.ErrNotExist
	}
	return buildCommandForProject(workspacePath, detected, target, extraArgs)
}

func buildCommandForProject(workspacePath string, detected projectType, target string, extraArgs []string) (string, []string, error) {
	sanitizedTarget := sanitizeTarget(target)
	sanitizedExtraArgs := sanitizeArgs(extraArgs)

	switch detected.Name {
	case "go":
		return buildGoCommand(repoKeyBuild, sanitizedTarget, sanitizedExtraArgs)
	case repoTypeNode:
		return buildNodeBuildCommand(sanitizedTarget, sanitizedExtraArgs)
	case repoTypePython:
		return buildPythonBuildCommand(sanitizedTarget, sanitizedExtraArgs)
	case repoTypeJava:
		return buildJavaBuildCommand(detected.Flavor, sanitizedExtraArgs)
	case repoTypeRust:
		return buildRustBuildCommand(sanitizedTarget, sanitizedExtraArgs)
	case "c":
		source, sourceErr := resolveCSourceForBuild(workspacePath, sanitizedTarget)
		if sourceErr != nil {
			return "", nil, sourceErr
		}
		args := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-o", ".workspace-c-build", source}
		args = append(args, sanitizedExtraArgs...)
		return "cc", args, nil
	default:
		return "", nil, os.ErrNotExist
	}
}

func buildGoCommand(verb, target string, extraArgs []string) (string, []string, error) {
	args := []string{verb}
	if target == "" {
		args = append(args, "./...")
	} else {
		args = append(args, target)
	}
	args = append(args, extraArgs...)
	return "go", args, nil
}

func buildNodeBuildCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{"run", repoKeyBuild, "--if-present"}
	if target != "" || len(extraArgs) > 0 {
		args = append(args, "--")
		if target != "" {
			args = append(args, target)
		}
		args = append(args, extraArgs...)
	}
	return "npm", args, nil
}

func buildPythonBuildCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{"-m", "compileall"}
	if target == "" {
		args = append(args, ".")
	} else {
		args = append(args, target)
	}
	args = append(args, extraArgs...)
	return "python", args, nil
}

func buildJavaBuildCommand(flavor string, extraArgs []string) (string, []string, error) {
	if flavor == repoBuildMaven {
		args := []string{"-q", "-DskipTests", "package"}
		args = append(args, extraArgs...)
		return "mvn", args, nil
	}
	args := []string{repoKeyBuild, "-x", "test"}
	args = append(args, extraArgs...)
	return repoBuildGradle, args, nil
}

func buildRustBuildCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{repoKeyBuild}
	if target != "" {
		args = append(args, repoFlagPackage, target)
	}
	args = append(args, extraArgs...)
	return repoBuildCargo, args, nil
}

func detectTestCommand(workspacePath, target string, extraArgs []string) (string, []string, error) {
	detected, ok := detectProjectTypeFromWorkspace(workspacePath)
	if !ok {
		return "", nil, os.ErrNotExist
	}
	return testCommandForProject(workspacePath, detected, target, extraArgs)
}

func testCommandForProject(workspacePath string, detected projectType, target string, extraArgs []string) (string, []string, error) {
	sanitizedTarget := sanitizeTarget(target)
	sanitizedExtraArgs := sanitizeArgs(extraArgs)

	switch detected.Name {
	case "go":
		return buildGoCommand("test", sanitizedTarget, sanitizedExtraArgs)
	case repoTypePython:
		return buildPytestCommand(sanitizedTarget, sanitizedExtraArgs)
	case repoTypeNode:
		return buildNodeTestCommand(sanitizedTarget, sanitizedExtraArgs)
	case repoTypeJava:
		return buildJavaTestCommand(detected.Flavor, sanitizedTarget, sanitizedExtraArgs)
	case repoTypeRust:
		return buildRustTestCommand(sanitizedTarget, sanitizedExtraArgs)
	case "c":
		source, sourceErr := resolveCSourceForTest(workspacePath, sanitizedTarget)
		if sourceErr != nil {
			return "", nil, sourceErr
		}
		args := []string{"-std=c11", "-O0", "-g", "-Wall", "-Wextra", "-o", ".workspace-c-test", source}
		args = append(args, sanitizedExtraArgs...)
		return "cc", args, nil
	default:
		return "", nil, os.ErrNotExist
	}
}

func buildPytestCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{"-q"}
	if target != "" {
		args = append(args, target)
	}
	args = append(args, extraArgs...)
	return "pytest", args, nil
}

func buildNodeTestCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{"test"}
	if target != "" || len(extraArgs) > 0 {
		args = append(args, "--")
		if target != "" {
			args = append(args, target)
		}
		args = append(args, extraArgs...)
	}
	return "npm", args, nil
}

func buildJavaTestCommand(flavor, target string, extraArgs []string) (string, []string, error) {
	if flavor == repoBuildMaven {
		args := []string{"-q", "test"}
		if target != "" {
			args = append(args, "-Dtest="+target)
		}
		args = append(args, extraArgs...)
		return "mvn", args, nil
	}
	args := []string{"test"}
	if target != "" {
		args = append(args, "--tests", target)
	}
	args = append(args, extraArgs...)
	return repoBuildGradle, args, nil
}

func buildRustTestCommand(target string, extraArgs []string) (string, []string, error) {
	args := []string{"test"}
	if target != "" {
		args = append(args, repoFlagPackage, target)
	}
	args = append(args, extraArgs...)
	return repoBuildCargo, args, nil
}

func sanitizeTarget(target string) string {
	trimmed := strings.TrimSpace(target)
	if strings.Contains(trimmed, "\x00") {
		return ""
	}
	return trimmed
}

func sanitizeArgs(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "\x00") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func filterRepoExtraArgs(detected projectType, extraArgs []string, mode string) ([]string, error) {
	args := sanitizeArgs(extraArgs)
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) > 8 {
		return nil, fmt.Errorf("extra_args exceeds max length (8)")
	}

	pendingValueFor := ""
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if len(arg) > 64 {
			return nil, fmt.Errorf("extra_args element too long")
		}
		if hasDangerousArgSyntax(arg) {
			return nil, fmt.Errorf("extra_args contains forbidden token")
		}

		if pendingValueFor != "" && !strings.HasPrefix(arg, "-") {
			filtered = append(filtered, arg)
			pendingValueFor = ""
			continue
		}
		pendingValueFor = ""

		nextValueExpected, allowed := allowRepoExtraArg(detected, mode, arg)
		if !allowed {
			return nil, fmt.Errorf("extra arg %q is not allowed for %s/%s", arg, detected.Name, mode)
		}
		if nextValueExpected {
			pendingValueFor = arg
		}
		filtered = append(filtered, arg)
	}
	if pendingValueFor != "" {
		return nil, fmt.Errorf("extra arg %q requires a value", pendingValueFor)
	}
	return filtered, nil
}

func hasDangerousArgSyntax(arg string) bool {
	if strings.Contains(arg, "\n") || strings.Contains(arg, "\r") {
		return true
	}
	dangerousTokens := []string{";", "|", "&", "`", "$(", ">", "<"}
	for _, token := range dangerousTokens {
		if strings.Contains(arg, token) {
			return true
		}
	}
	return false
}

func allowRepoExtraArg(detected projectType, mode string, arg string) (expectsValue bool, allowed bool) {
	switch detected.Name {
	case "go":
		return allowGoExtraArg(arg)
	case repoTypePython:
		if mode == "test" {
			return allowPythonTestExtraArg(arg)
		}
		return false, false
	case repoTypeRust:
		return allowRustExtraArg(arg)
	case repoTypeJava:
		return allowJavaExtraArg(detected.Flavor, mode, arg)
	case repoTypeNode:
		return false, false
	case "c":
		return false, false
	default:
		return false, false
	}
}

func allowGoExtraArg(arg string) (bool, bool) {
	denyPrefixes := []string{"-exec", "-toolexec", "-mod=mod", "-modfile", "-overlay", "-buildmode=plugin"}
	for _, denied := range denyPrefixes {
		if strings.HasPrefix(arg, denied) {
			return false, false
		}
	}
	allowedExact := map[string]struct{}{
		"-v":            {},
		"-x":            {},
		"-race":         {},
		"-trimpath":     {},
		"-short":        {},
		"-cover":        {},
		"-mod=readonly": {},
		"-mod=vendor":   {},
	}
	if _, ok := allowedExact[arg]; ok {
		return false, true
	}
	valueFlags := map[string]struct{}{
		"-tags":         {},
		"-run":          {},
		"-count":        {},
		"-timeout":      {},
		"-coverprofile": {},
	}
	if _, ok := valueFlags[arg]; ok {
		return true, true
	}
	allowedPrefixes := []string{"-tags=", "-run=", "-count=", "-timeout=", "-coverprofile="}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(arg, prefix) {
			return false, true
		}
	}
	return false, false
}

func allowPythonTestExtraArg(arg string) (bool, bool) {
	allowedExact := map[string]struct{}{
		"-q": {},
		"-x": {},
		"-s": {},
	}
	if _, ok := allowedExact[arg]; ok {
		return false, true
	}
	valueFlags := map[string]struct{}{
		"-k":        {},
		"-m":        {},
		"--maxfail": {},
	}
	if _, ok := valueFlags[arg]; ok {
		return true, true
	}
	allowedPrefixes := []string{"-k=", "-m=", "--maxfail="}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(arg, prefix) {
			return false, true
		}
	}
	return false, false
}

func allowRustExtraArg(arg string) (bool, bool) {
	if strings.HasPrefix(arg, "-Z") {
		return false, false
	}
	allowedExact := map[string]struct{}{
		"--release":             {},
		"--locked":              {},
		"--offline":             {},
		"--all-features":        {},
		"--no-default-features": {},
	}
	if _, ok := allowedExact[arg]; ok {
		return false, true
	}
	valueFlags := map[string]struct{}{
		"--features":    {},
		repoFlagPackage: {},
		"--jobs":        {},
		"--target":      {},
	}
	if _, ok := valueFlags[arg]; ok {
		return true, true
	}
	allowedPrefixes := []string{"--features=", "--package=", "--jobs=", "--target="}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(arg, prefix) {
			return false, true
		}
	}
	return false, false
}

func allowJavaExtraArg(flavor, mode, arg string) (bool, bool) {
	if flavor == repoBuildMaven {
		return allowMavenExtraArg(arg)
	}
	return allowGradleExtraArg(mode, arg)
}

func allowMavenExtraArg(arg string) (bool, bool) {
	if strings.HasPrefix(arg, "-D") {
		return allowMavenPropertyArg(arg)
	}
	if arg == "--batch-mode" || arg == "--no-transfer-progress" {
		return false, true
	}
	if arg == "-P" {
		return true, true
	}
	if strings.HasPrefix(arg, "-P") {
		return false, true
	}
	return false, false
}

func allowMavenPropertyArg(arg string) (bool, bool) {
	allowedProps := []string{"-DskipTests", "-DskipITs", "-Dtest="}
	for _, prefix := range allowedProps {
		if arg == prefix || strings.HasPrefix(arg, prefix) {
			return false, true
		}
	}
	return false, false
}

func allowGradleExtraArg(mode, arg string) (bool, bool) {
	allowedExact := map[string]struct{}{
		"--no-daemon":  {},
		"--info":       {},
		"--stacktrace": {},
	}
	if _, ok := allowedExact[arg]; ok {
		return false, true
	}
	if mode == "test" && arg == "--tests" {
		return true, true
	}
	if strings.HasPrefix(arg, "--tests=") {
		return false, true
	}
	return false, false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasCSourceFiles(workspacePath string, includeTests bool) bool {
	return len(findCSourceFiles(workspacePath, includeTests)) > 0
}

func resolveCSourceForBuild(workspacePath, requested string) (string, error) {
	if requested != "" {
		resolved, err := sanitizeRelativePath(requested)
		if err != nil {
			return "", err
		}
		if !strings.HasSuffix(strings.ToLower(resolved), ".c") {
			return "", fmt.Errorf("c build target must be a .c source file")
		}
		// For Kubernetes-backed workspaces, the service process cannot stat remote files.
		// Defer existence checks to the compiler invocation inside the workspace runtime.
		return resolved, nil
	}

	if exists(filepath.Join(workspacePath, "main.c")) {
		return "main.c", nil
	}

	files := findCSourceFiles(workspacePath, false)
	if len(files) == 0 {
		return "", os.ErrNotExist
	}
	return files[0], nil
}

func resolveCSourceForTest(workspacePath, requested string) (string, error) {
	if requested != "" {
		resolved, err := sanitizeRelativePath(requested)
		if err != nil {
			return "", err
		}
		if !strings.HasSuffix(strings.ToLower(resolved), ".c") {
			return "", fmt.Errorf("c test target must be a .c source file")
		}
		// For Kubernetes-backed workspaces, the service process cannot stat remote files.
		// Defer existence checks to the compiler invocation inside the workspace runtime.
		return resolved, nil
	}

	testFiles := findCSourceFiles(workspacePath, true)
	for _, candidate := range testFiles {
		if strings.HasSuffix(candidate, "_test.c") || strings.HasSuffix(candidate, "test.c") {
			return candidate, nil
		}
	}
	if len(testFiles) > 0 {
		return testFiles[0], nil
	}
	return "", os.ErrNotExist
}

func findCSourceFiles(workspacePath string, includeTests bool) []string {
	out := make([]string, 0)
	_ = filepath.WalkDir(workspacePath, func(path string, entry fs.DirEntry, err error) error {
		return walkCSourceEntry(workspacePath, path, entry, err, includeTests, &out)
	})
	sort.Strings(out)
	return out
}

func walkCSourceEntry(workspacePath, path string, entry fs.DirEntry, err error, includeTests bool, out *[]string) error {
	if err != nil {
		return nil
	}
	if entry.IsDir() {
		return walkCSourceDir(workspacePath, path, entry)
	}
	if strings.ToLower(filepath.Ext(entry.Name())) != ".c" {
		return nil
	}
	if !includeTests && isCTestFile(entry.Name()) {
		return nil
	}
	rel, relErr := filepath.Rel(workspacePath, path)
	if relErr != nil {
		return nil
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") {
		return nil
	}
	*out = append(*out, clean)
	return nil
}

func walkCSourceDir(workspacePath, path string, entry fs.DirEntry) error {
	rel, relErr := filepath.Rel(workspacePath, path)
	if relErr != nil {
		return nil
	}
	if rel != "." && strings.Count(rel, string(filepath.Separator)) >= 3 {
		return filepath.SkipDir
	}
	if strings.HasPrefix(entry.Name(), ".git") || entry.Name() == "node_modules" || entry.Name() == "vendor" {
		return filepath.SkipDir
	}
	return nil
}

func isCTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.c") || strings.HasSuffix(name, "test.c")
}

func sanitizeRelativePath(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}
	if strings.Contains(trimmed, "\x00") {
		return "", fmt.Errorf("invalid path")
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be workspace-relative")
	}
	return filepath.ToSlash(clean), nil
}
