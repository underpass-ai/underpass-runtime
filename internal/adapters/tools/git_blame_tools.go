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

// GitBlameHandler returns line-by-line blame info for a file.
type GitBlameHandler struct {
	runner app.CommandRunner
}

func NewGitBlameHandler(runner app.CommandRunner) *GitBlameHandler {
	return &GitBlameHandler{runner: runner}
}

func (h *GitBlameHandler) Name() string { return "git.blame" }

func (h *GitBlameHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid git.blame args", Retryable: false}
	}
	if strings.TrimSpace(request.Path) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "path is required", Retryable: false}
	}
	if _, pathErr := resolvePath(session, request.Path); pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	command := []string{"blame", "-c"}
	if request.StartLine > 0 && request.EndLine >= request.StartLine {
		command = append(command, fmt.Sprintf("-L%d,%d", request.StartLine, request.EndLine))
	}
	command = append(command, "--", request.Path)

	commandResult, runErr := executeGit(ctx, h.runner, session, command, nil, 512*1024)
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Output: map[string]any{
			gitKeyCommand: append([]string{"git"}, command...),
			"path":        request.Path,
			"blame":       commandResult.Output,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: gitKeyStdout, Message: commandResult.Output}},
	}
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}
