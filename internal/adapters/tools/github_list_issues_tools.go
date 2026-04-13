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

// GitHubListIssuesHandler lists GitHub issues with optional filters.
type GitHubListIssuesHandler struct {
	runner app.CommandRunner
}

func NewGitHubListIssuesHandler(runner app.CommandRunner) *GitHubListIssuesHandler {
	return &GitHubListIssuesHandler{runner: runner}
}

func (h *GitHubListIssuesHandler) Name() string { return "github.list_issues" }

func (h *GitHubListIssuesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		State  string `json:"state"`
		Labels string `json:"labels"`
		Limit  int    `json:"limit"`
		Repo   string `json:"repo"`
	}{State: "open", Limit: 10}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.list_issues args", Retryable: false}
	}
	if request.Limit <= 0 {
		request.Limit = 10
	}
	if request.Limit > 50 {
		request.Limit = 50
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	ghArgs := []string{"issue", "list", "--json", "number,title,state,labels,assignees,createdAt",
		"--state", request.State, "--limit", fmt.Sprintf("%d", request.Limit)}
	if strings.TrimSpace(request.Labels) != "" {
		ghArgs = append(ghArgs, "--label", request.Labels)
	}
	if strings.TrimSpace(request.Repo) != "" {
		ghArgs = append(ghArgs, "--repo", request.Repo)
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd: session.WorkspacePath, Command: "gh", Args: ghArgs, MaxBytes: 256 * 1024,
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("gh issue list failed: %v", runErr), Retryable: true}
	}

	var issues []any
	if json.Unmarshal([]byte(commandResult.Output), &issues) != nil {
		issues = nil
	}

	return app.ToolRunResult{
		Output:   map[string]any{"issues": issues, "count": len(issues), "state": request.State},
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: ghIssueKeyStdout, Message: fmt.Sprintf("listed %d issues", len(issues))}},
	}, nil
}
