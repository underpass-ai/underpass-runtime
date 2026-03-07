package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	npmIfPresent          = "--if-present"
	workspaceVenvDir      = ".workspace-venv"
	artifactNodeBuild     = "node-build-output.txt"
	artifactPythonInstall = "python-install-output.txt"
	artifactCTest         = "c-test-output.txt"
	langRunnerCargo       = "cargo"
	langRunnerPython      = "python"
	langRunnerPytest      = "pytest"
)

type RustBuildHandler struct {
	runner app.CommandRunner
}

type RustTestHandler struct {
	runner app.CommandRunner
}

type RustClippyHandler struct {
	runner app.CommandRunner
}

type RustFormatHandler struct {
	runner app.CommandRunner
}

type NodeInstallHandler struct {
	runner app.CommandRunner
}

type NodeBuildHandler struct {
	runner app.CommandRunner
}

type NodeTestHandler struct {
	runner app.CommandRunner
}

type NodeLintHandler struct {
	runner app.CommandRunner
}

type NodeTypecheckHandler struct {
	runner app.CommandRunner
}

type PythonInstallDepsHandler struct {
	runner app.CommandRunner
}

type PythonValidateHandler struct {
	runner app.CommandRunner
}

type PythonTestHandler struct {
	runner app.CommandRunner
}

type CBuildHandler struct {
	runner app.CommandRunner
}

type CTestHandler struct {
	runner app.CommandRunner
}

func NewRustBuildHandler(runner app.CommandRunner) *RustBuildHandler {
	return &RustBuildHandler{runner: runner}
}

func NewRustTestHandler(runner app.CommandRunner) *RustTestHandler {
	return &RustTestHandler{runner: runner}
}

func NewRustClippyHandler(runner app.CommandRunner) *RustClippyHandler {
	return &RustClippyHandler{runner: runner}
}

func NewRustFormatHandler(runner app.CommandRunner) *RustFormatHandler {
	return &RustFormatHandler{runner: runner}
}

func NewNodeInstallHandler(runner app.CommandRunner) *NodeInstallHandler {
	return &NodeInstallHandler{runner: runner}
}

func NewNodeBuildHandler(runner app.CommandRunner) *NodeBuildHandler {
	return &NodeBuildHandler{runner: runner}
}

func NewNodeTestHandler(runner app.CommandRunner) *NodeTestHandler {
	return &NodeTestHandler{runner: runner}
}

func NewNodeLintHandler(runner app.CommandRunner) *NodeLintHandler {
	return &NodeLintHandler{runner: runner}
}

func NewNodeTypecheckHandler(runner app.CommandRunner) *NodeTypecheckHandler {
	return &NodeTypecheckHandler{runner: runner}
}

func NewPythonInstallDepsHandler(runner app.CommandRunner) *PythonInstallDepsHandler {
	return &PythonInstallDepsHandler{runner: runner}
}

func NewPythonValidateHandler(runner app.CommandRunner) *PythonValidateHandler {
	return &PythonValidateHandler{runner: runner}
}

func NewPythonTestHandler(runner app.CommandRunner) *PythonTestHandler {
	return &PythonTestHandler{runner: runner}
}

func NewCBuildHandler(runner app.CommandRunner) *CBuildHandler {
	return &CBuildHandler{runner: runner}
}

func NewCTestHandler(runner app.CommandRunner) *CTestHandler {
	return &CTestHandler{runner: runner}
}

func (h *RustBuildHandler) Name() string {
	return "rust.build"
}

func (h *RustBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target  string `json:"target"`
		Release bool   `json:"release"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid rust.build args", Retryable: false}
		}
	}

	commandArgs := []string{"build"}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--package", target)
	}
	if request.Release {
		commandArgs = append(commandArgs, "--release")
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: langRunnerCargo, args: commandArgs, artifactName: "rust-build-output.txt"})
}

func (h *RustTestHandler) Name() string {
	return "rust.test"
}

func (h *RustTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid rust.test args", Retryable: false}
		}
	}

	commandArgs := []string{"test"}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--package", target)
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: langRunnerCargo, args: commandArgs, artifactName: "rust-test-output.txt"})
}

func (h *RustClippyHandler) Name() string {
	return "rust.clippy"
}

func (h *RustClippyHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		DenyWarnings bool `json:"deny_warnings"`
	}{DenyWarnings: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid rust.clippy args", Retryable: false}
		}
	}

	commandArgs := []string{"clippy", "--all-targets", "--all-features"}
	if request.DenyWarnings {
		commandArgs = append(commandArgs, "--", "-D", "warnings")
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: langRunnerCargo, args: commandArgs, artifactName: "rust-clippy-output.txt"})
}

func (h *RustFormatHandler) Name() string {
	return "rust.format"
}

func (h *RustFormatHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Check bool `json:"check"`
	}{Check: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid rust.format args", Retryable: false}
		}
	}

	commandArgs := []string{"fmt"}
	if request.Check {
		commandArgs = append(commandArgs, "--", "--check")
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: langRunnerCargo, args: commandArgs, artifactName: "rust-format-output.txt"})
}

func (h *NodeInstallHandler) Name() string {
	return "node.install"
}

func (h *NodeInstallHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		UseCI         bool `json:"use_ci"`
		IgnoreScripts bool `json:"ignore_scripts"`
	}{UseCI: true, IgnoreScripts: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid node.install args", Retryable: false}
		}
	}

	commandArgs := []string{"ci"}
	if !request.UseCI {
		commandArgs = []string{"install"}
	}
	if request.IgnoreScripts {
		commandArgs = append(commandArgs, "--ignore-scripts")
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "npm", args: commandArgs, artifactName: "node-install-output.txt"})
}

func (h *NodeBuildHandler) Name() string {
	return "node.build"
}

func (h *NodeBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid node.build args", Retryable: false}
		}
	}

	commandArgs := []string{"run", "build", npmIfPresent}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--", target)
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "npm", args: commandArgs, artifactName: artifactNodeBuild})
}

func (h *NodeTestHandler) Name() string {
	return "node.test"
}

func (h *NodeTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid node.test args", Retryable: false}
		}
	}

	commandArgs := []string{"run", "test", npmIfPresent}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--", target)
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "npm", args: commandArgs, artifactName: "node-test-output.txt"})
}

func (h *NodeLintHandler) Name() string {
	return "node.lint"
}

func (h *NodeLintHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid node.lint args", Retryable: false}
		}
	}

	commandArgs := []string{"run", "lint", npmIfPresent}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--", target)
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "npm", args: commandArgs, artifactName: "node-lint-output.txt"})
}

func (h *NodeTypecheckHandler) Name() string {
	return "node.typecheck"
}

func (h *NodeTypecheckHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid node.typecheck args", Retryable: false}
		}
	}

	commandArgs := []string{"run", "typecheck", npmIfPresent}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, "--", target)
	}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "npm", args: commandArgs, artifactName: "node-typecheck-output.txt"})
}

func (h *PythonInstallDepsHandler) Name() string {
	return "python.install_deps"
}

func (h *PythonInstallDepsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		UseVenv          bool   `json:"use_venv"`
		RequirementsFile string `json:"requirements_file"`
		ConstraintsFile  string `json:"constraints_file"`
	}{UseVenv: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid python.install_deps args", Retryable: false}
		}
	}
	if !request.UseVenv {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "python.install_deps requires use_venv=true",
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	venvPath := workspaceVenvDir
	setupResult, setupErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "python3",
		Args:     []string{"-m", "venv", venvPath},
		MaxBytes: 1024 * 1024,
	})
	if setupErr != nil {
		result := structuredToolRunResult(setupResult.ExitCode, setupResult.Output, artifactPythonInstall, "", 0.0)
		return result, toToolError(setupErr, setupResult.Output)
	}

	installArgs, earlyResult, resolveErr := resolvePipInstallArgs(session.WorkspacePath, request.RequirementsFile, request.ConstraintsFile)
	if resolveErr != nil {
		return app.ToolRunResult{}, resolveErr
	}
	if earlyResult != nil {
		return *earlyResult, nil
	}

	pythonExecutable := filepath.ToSlash(filepath.Join(venvPath, "bin", langRunnerPython))
	return invokeStructuredToolCommand(ctx, runner, session, structuredToolParams{command: pythonExecutable, args: installArgs, artifactName: artifactPythonInstall})
}

func resolvePipInstallArgs(workspacePath, requirementsFile, constraintsFile string) ([]string, *app.ToolRunResult, *domain.Error) {
	requirementsPath := strings.TrimSpace(requirementsFile)
	if requirementsPath == "" && exists(filepath.Join(workspacePath, "requirements.txt")) {
		requirementsPath = "requirements.txt"
	}
	if requirementsPath == "" {
		diagnostic := "no requirements file found; dependencies skipped"
		result := structuredToolRunResult(0, diagnostic, artifactPythonInstall, "", 0.0)
		result.Output.(map[string]any)["diagnostics"] = []string{diagnostic}
		return nil, &result, nil
	}

	requirementsRelative, reqErr := sanitizeRelativePath(requirementsPath)
	if reqErr != nil {
		return nil, nil, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: reqErr.Error(), Retryable: false}
	}
	if !exists(filepath.Join(workspacePath, filepath.FromSlash(requirementsRelative))) {
		return nil, nil, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "requirements_file not found", Retryable: false}
	}

	constraintsRelative, constraintsErr := resolveConstraintsFile(workspacePath, constraintsFile)
	if constraintsErr != nil {
		return nil, nil, constraintsErr
	}

	installArgs := []string{"-m", "pip", "install", "--disable-pip-version-check", "-r", requirementsRelative}
	if constraintsRelative != "" {
		installArgs = append(installArgs, "-c", constraintsRelative)
	}
	return installArgs, nil, nil
}

func resolveConstraintsFile(workspacePath, constraintsFile string) (string, *domain.Error) {
	if strings.TrimSpace(constraintsFile) == "" {
		return "", nil
	}
	resolvedConstraints, constraintsErr := sanitizeRelativePath(constraintsFile)
	if constraintsErr != nil {
		return "", &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: constraintsErr.Error(), Retryable: false}
	}
	if !exists(filepath.Join(workspacePath, filepath.FromSlash(resolvedConstraints))) {
		return "", &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "constraints_file not found", Retryable: false}
	}
	return resolvedConstraints, nil
}

func (h *PythonValidateHandler) Name() string {
	return "python.validate"
}

func (h *PythonValidateHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid python.validate args", Retryable: false}
		}
	}

	target := sanitizeTarget(request.Target)
	if target == "" {
		target = "."
	}
	pythonExecutable := resolvePythonExecutable(session.WorkspacePath)
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: pythonExecutable, args: []string{"-m", "compileall", target}, artifactName: "python-validate-output.txt"})
}

func (h *PythonTestHandler) Name() string {
	return "python.test"
}

func (h *PythonTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target     string `json:"target"`
		RunPattern string `json:"run_pattern"`
		MaxFail    int    `json:"max_fail"`
	}{MaxFail: 1}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid python.test args", Retryable: false}
		}
	}

	if request.MaxFail <= 0 {
		request.MaxFail = 1
	}
	if request.MaxFail > 20 {
		request.MaxFail = 20
	}
	if strings.Contains(request.RunPattern, "\x00") || len(request.RunPattern) > 256 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid run_pattern", Retryable: false}
	}

	commandArgs := []string{"-q", "--maxfail", fmt.Sprintf("%d", request.MaxFail)}
	target := sanitizeTarget(request.Target)
	if target != "" {
		commandArgs = append(commandArgs, target)
	}
	if strings.TrimSpace(request.RunPattern) != "" {
		commandArgs = append(commandArgs, "-k", strings.TrimSpace(request.RunPattern))
	}

	pytestExecutable := resolvePytestExecutable(session.WorkspacePath)
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: pytestExecutable, args: commandArgs, artifactName: "python-test-output.txt"})
}

func (h *CBuildHandler) Name() string {
	return "c.build"
}

func (h *CBuildHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Source     string `json:"source"`
		OutputName string `json:"output_name"`
		Standard   string `json:"standard"`
	}{Standard: "c11"}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid c.build args", Retryable: false}
		}
	}

	standard, standardErr := sanitizeCStandard(request.Standard)
	if standardErr != nil {
		return app.ToolRunResult{}, standardErr
	}
	outputName, outputErr := sanitizeOutputName(request.OutputName)
	if outputErr != nil {
		return app.ToolRunResult{}, outputErr
	}
	if outputName == "" {
		outputName = "c-app"
	}

	source, sourceErr := resolveCSourceForBuild(session.WorkspacePath, sanitizeTarget(request.Source))
	if sourceErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("cannot resolve c source: %v", sourceErr),
			Retryable: false,
		}
	}

	commandArgs := []string{"-std=" + standard, "-O2", "-Wall", "-Wextra", "-o", outputName, source}
	return invokeStructuredToolCommand(ctx, h.runner, session, structuredToolParams{command: "cc", args: commandArgs, artifactName: "c-build-output.txt", compiledBinaryPath: outputName})
}

func (h *CTestHandler) Name() string {
	return "c.test"
}

func (h *CTestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Source     string `json:"source"`
		OutputName string `json:"output_name"`
		Standard   string `json:"standard"`
		Run        bool   `json:"run"`
	}{Standard: "c11", Run: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid c.test args", Retryable: false}
		}
	}

	standard, standardErr := sanitizeCStandard(request.Standard)
	if standardErr != nil {
		return app.ToolRunResult{}, standardErr
	}
	outputName, outputErr := sanitizeOutputName(request.OutputName)
	if outputErr != nil {
		return app.ToolRunResult{}, outputErr
	}
	if outputName == "" {
		outputName = "c-test"
	}

	source, sourceErr := resolveCSourceForTest(session.WorkspacePath, sanitizeTarget(request.Source))
	if sourceErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("cannot resolve c test source: %v", sourceErr),
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	compileResult, compileErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "cc",
		Args:     []string{"-std=" + standard, "-O0", "-g", "-Wall", "-Wextra", "-o", outputName, source},
		MaxBytes: 2 * 1024 * 1024,
	})
	if compileErr != nil {
		result := structuredToolRunResult(compileResult.ExitCode, compileResult.Output, artifactCTest, outputName, 0.0)
		return result, toToolError(compileErr, compileResult.Output)
	}
	if !request.Run {
		return structuredToolRunResult(compileResult.ExitCode, compileResult.Output, artifactCTest, outputName, 0.0), nil
	}

	execResult, execErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "./" + outputName,
		Args:     []string{},
		MaxBytes: 2 * 1024 * 1024,
	})
	combinedOutput := strings.TrimSpace(compileResult.Output + "\n" + execResult.Output)
	result := structuredToolRunResult(execResult.ExitCode, combinedOutput, artifactCTest, outputName, 0.0)
	if execErr != nil {
		return result, toToolError(execErr, execResult.Output)
	}
	return result, nil
}

type structuredToolParams struct {
	command            string
	args               []string
	artifactName       string
	compiledBinaryPath string
	coveragePercent    float64
}

func invokeStructuredToolCommand(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	p structuredToolParams,
) (app.ToolRunResult, *domain.Error) {
	commandResult, runErr := ensureRunner(runner).Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  p.command,
		Args:     p.args,
		MaxBytes: 2 * 1024 * 1024,
	})
	result := structuredToolRunResult(commandResult.ExitCode, commandResult.Output, p.artifactName, p.compiledBinaryPath, p.coveragePercent)
	if runErr != nil {
		return result, toToolError(runErr, commandResult.Output)
	}
	return result, nil
}

func structuredToolRunResult(
	exitCode int,
	output string,
	artifactName string,
	compiledBinaryPath string,
	coveragePercent float64,
) app.ToolRunResult {
	if strings.TrimSpace(artifactName) == "" {
		artifactName = "tool-output.txt"
	}
	return app.ToolRunResult{
		ExitCode: exitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: output}},
		Output: map[string]any{
			"exit_code":            exitCode,
			"compiled_binary_path": compiledBinaryPath,
			"coverage_percent":     coveragePercent,
			"diagnostics":          extractDiagnostics(output, 80),
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        artifactName,
			ContentType: "text/plain",
			Data:        []byte(output),
		}},
	}
}

func sanitizeCStandard(raw string) (string, *domain.Error) {
	standard := strings.ToLower(strings.TrimSpace(raw))
	if standard == "" {
		standard = "c11"
	}
	switch standard {
	case "c89", "c99", "c11", "c17", "c23":
		return standard, nil
	default:
		return "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "unsupported c standard",
			Retryable: false,
		}
	}
}

func resolvePythonExecutable(workspacePath string) string {
	candidate := filepath.Join(workspacePath, workspaceVenvDir, "bin", langRunnerPython)
	if exists(candidate) {
		return filepath.ToSlash(filepath.Join(workspaceVenvDir, "bin", langRunnerPython))
	}
	return "python3"
}

func resolvePytestExecutable(workspacePath string) string {
	candidate := filepath.Join(workspacePath, workspaceVenvDir, "bin", langRunnerPytest)
	if exists(candidate) {
		return filepath.ToSlash(filepath.Join(workspaceVenvDir, "bin", langRunnerPytest))
	}
	return langRunnerPytest
}
