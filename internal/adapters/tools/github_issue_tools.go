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
	ghIssueKeyStdout = "stdout"
)

// GitHubGetIssueHandler reads a GitHub issue by number. Completes the
// issue→code→PR loop — agents can read the issue that triggered their work.
type GitHubGetIssueHandler struct {
	runner app.CommandRunner
}

func NewGitHubGetIssueHandler(runner app.CommandRunner) *GitHubGetIssueHandler {
	return &GitHubGetIssueHandler{runner: runner}
}

func (h *GitHubGetIssueHandler) Name() string {
	return "github.get_issue"
}

func (h *GitHubGetIssueHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Number int    `json:"number"`
		Repo   string `json:"repo"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.get_issue args", Retryable: false}
	}
	if request.Number <= 0 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "issue number is required", Retryable: false}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	ghArgs := []string{"issue", "view", fmt.Sprintf("%d", request.Number), "--json",
		"number,title,body,state,labels,assignees,comments,createdAt,updatedAt,author"}
	if strings.TrimSpace(request.Repo) != "" {
		ghArgs = append(ghArgs, "--repo", request.Repo)
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "gh",
		Args:     ghArgs,
		MaxBytes: 256 * 1024,
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("gh issue view failed: %v", runErr), Retryable: true,
		}
	}

	var issue map[string]any
	if json.Unmarshal([]byte(commandResult.Output), &issue) != nil {
		// Return raw output if not JSON.
		return app.ToolRunResult{
			Output:   map[string]any{"number": request.Number, "raw": commandResult.Output},
			ExitCode: commandResult.ExitCode,
			Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: ghIssueKeyStdout, Message: commandResult.Output}},
		}, nil
	}

	return app.ToolRunResult{
		Output:   issue,
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: ghIssueKeyStdout, Message: fmt.Sprintf("issue #%d: %v", request.Number, issue["title"])}},
	}, nil
}
