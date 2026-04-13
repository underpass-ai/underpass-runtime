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

// GitHubReviewCommentsHandler reads review comments from a PR.
type GitHubReviewCommentsHandler struct {
	runner app.CommandRunner
}

func NewGitHubReviewCommentsHandler(runner app.CommandRunner) *GitHubReviewCommentsHandler {
	return &GitHubReviewCommentsHandler{runner: runner}
}

func (h *GitHubReviewCommentsHandler) Name() string { return "github.review_comments" }

func (h *GitHubReviewCommentsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Branch string `json:"branch"`
		Repo   string `json:"repo"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.review_comments args", Retryable: false}
	}
	if strings.TrimSpace(request.Branch) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "branch is required", Retryable: false}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	ghArgs := []string{"pr", "view", request.Branch, "--json", "reviews,comments,reviewDecision"}
	if strings.TrimSpace(request.Repo) != "" {
		ghArgs = append(ghArgs, "--repo", request.Repo)
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd: session.WorkspacePath, Command: "gh", Args: ghArgs, MaxBytes: 256 * 1024,
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("gh pr view failed: %v", runErr), Retryable: true}
	}

	var prData map[string]any
	if json.Unmarshal([]byte(commandResult.Output), &prData) != nil {
		prData = map[string]any{"raw": commandResult.Output}
	}

	return app.ToolRunResult{
		Output:   prData,
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: ghIssueKeyStdout, Message: fmt.Sprintf("review comments for %s", request.Branch)}},
	}, nil
}
