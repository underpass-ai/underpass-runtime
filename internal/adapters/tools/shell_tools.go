package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	shellMaxCommandLen    = 4096
	shellMaxStdinBytes    = 1024 * 1024
	shellMaxOutputBytes   = 1024 * 1024
	shellDefaultTimeoutS  = 60
	shellMaxTimeoutS      = 600
	shellKeyStdout        = "stdout"
	shellErrCommandEmpty  = "command is required"
	shellErrCommandTooLng = "command exceeds maximum length"
)

// ShellExecHandler executes a shell command inside the workspace with policy
// governance. The command runs via `sh -lc` (login shell) in the session's
// workspace directory.
type ShellExecHandler struct {
	runner app.CommandRunner
}

func NewShellExecHandler(runner app.CommandRunner) *ShellExecHandler {
	return &ShellExecHandler{runner: runner}
}

func (h *ShellExecHandler) Name() string {
	return "shell.exec"
}

func (h *ShellExecHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Command        string `json:"command"`
		Stdin          string `json:"stdin"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Cwd            string `json:"cwd"`
	}{
		TimeoutSeconds: shellDefaultTimeoutS,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: "invalid shell.exec args", Retryable: false,
		}
	}

	command := strings.TrimSpace(request.Command)
	if command == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: shellErrCommandEmpty, Retryable: false,
		}
	}
	if len(command) > shellMaxCommandLen {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: shellErrCommandTooLng, Retryable: false,
		}
	}

	// Resolve working directory — default to workspace root, allow relative subdir.
	cwd := session.WorkspacePath
	if sub := strings.TrimSpace(request.Cwd); sub != "" {
		resolved, pathErr := resolvePath(session, sub)
		if pathErr != nil {
			return app.ToolRunResult{}, pathErr
		}
		cwd = resolved
	}

	timeout := clampShellTimeout(request.TimeoutSeconds)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var stdin []byte
	if request.Stdin != "" {
		if len(request.Stdin) > shellMaxStdinBytes {
			return app.ToolRunResult{}, &domain.Error{
				Code: app.ErrorCodeInvalidArgument, Message: "stdin exceeds 1MB limit", Retryable: false,
			}
		}
		stdin = []byte(request.Stdin)
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      cwd,
		Command:  "sh",
		Args:     []string{"-lc", command},
		Stdin:    stdin,
		MaxBytes: shellMaxOutputBytes,
	})

	output := map[string]any{
		"command":   command,
		"exit_code": commandResult.ExitCode,
		"stdout":    commandResult.Output,
	}

	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Output:   output,
		Logs: []domain.LogLine{
			{At: time.Now().UTC(), Channel: shellKeyStdout, Message: commandResult.Output},
		},
	}

	if runErr != nil {
		if strings.Contains(runErr.Error(), "timeout") || ctx.Err() != nil {
			return result, &domain.Error{
				Code: app.ErrorCodeTimeout, Message: fmt.Sprintf("command timed out after %ds", timeout), Retryable: true,
			}
		}
		// Non-zero exit code is not an error — return the result with exit code.
		// Only return error for infrastructure failures (runner unreachable, etc.)
		if commandResult.ExitCode != 0 {
			return result, nil
		}
		return result, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: runErr.Error(), Retryable: false,
		}
	}

	return result, nil
}

func clampShellTimeout(seconds int) int {
	if seconds <= 0 {
		return shellDefaultTimeoutS
	}
	if seconds > shellMaxTimeoutS {
		return shellMaxTimeoutS
	}
	return seconds
}
