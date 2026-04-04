package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ---------------------------------------------------------------------------
// github.create_pr
// ---------------------------------------------------------------------------

type GitHubCreatePRHandler struct {
	runner app.CommandRunner
}

func NewGitHubCreatePRHandler(runner app.CommandRunner) *GitHubCreatePRHandler {
	return &GitHubCreatePRHandler{runner: runner}
}

func (h *GitHubCreatePRHandler) Name() string { return "github.create_pr" }

func (h *GitHubCreatePRHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Base  string `json:"base"`
		Head  string `json:"head"`
	}{Base: "main"}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.create_pr args", Retryable: false}
	}
	if strings.TrimSpace(request.Title) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "title is required", Retryable: false}
	}
	if strings.TrimSpace(request.Head) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "head branch is required", Retryable: false}
	}

	result, err := h.runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "gh",
		Args:     []string{"pr", "create", "--title", request.Title, "--body", request.Body, "--base", request.Base, "--head", request.Head},
		MaxBytes: 64 * 1024,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("gh pr create failed: %v", err), Retryable: true}
	}

	return app.ToolRunResult{
		Output:   map[string]any{"pr_url": strings.TrimSpace(result.Output), "base": request.Base, "head": request.Head},
		ExitCode: result.ExitCode,
	}, nil
}

// ---------------------------------------------------------------------------
// github.check_pr_status
// ---------------------------------------------------------------------------

type GitHubCheckPRStatusHandler struct {
	runner app.CommandRunner
}

func NewGitHubCheckPRStatusHandler(runner app.CommandRunner) *GitHubCheckPRStatusHandler {
	return &GitHubCheckPRStatusHandler{runner: runner}
}

func (h *GitHubCheckPRStatusHandler) Name() string { return "github.check_pr_status" }

func (h *GitHubCheckPRStatusHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Branch         string   `json:"branch"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		RequiredChecks []string `json:"required_checks"`
	}{TimeoutSeconds: 300}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.check_pr_status args", Retryable: false}
	}
	if strings.TrimSpace(request.Branch) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "branch is required", Retryable: false}
	}

	deadline := time.Now().Add(time.Duration(request.TimeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		result, err := h.runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "gh",
			Args:     []string{"pr", "checks", request.Branch, "--json", "name,state,conclusion"},
			MaxBytes: 64 * 1024,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeTimeout, Message: "context cancelled while waiting for checks", Retryable: false}
			case <-time.After(10 * time.Second):
				continue
			}
		}

		var checks []struct {
			Name       string `json:"name"`
			State      string `json:"state"`
			Conclusion string `json:"conclusion"`
		}
		if err := json.Unmarshal([]byte(result.Output), &checks); err != nil {
			select {
			case <-ctx.Done():
				return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeTimeout, Message: "context cancelled while waiting for checks", Retryable: false}
			case <-time.After(10 * time.Second):
				continue
			}
		}

		allPassed := true
		anyFailed := false
		for _, check := range checks {
			if check.State != "COMPLETED" {
				allPassed = false
				continue
			}
			if check.Conclusion == "FAILURE" || check.Conclusion == "CANCELLED" {
				anyFailed = true
			}
		}

		if anyFailed {
			return app.ToolRunResult{Output: map[string]any{"status": "failed", "checks": checks}, ExitCode: 1}, nil
		}
		if allPassed && len(checks) > 0 {
			return app.ToolRunResult{Output: map[string]any{"status": "passed", "checks": checks}, ExitCode: 0}, nil
		}

		select {
		case <-ctx.Done():
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeTimeout, Message: "context cancelled while waiting for checks", Retryable: false}
		case <-time.After(10 * time.Second):
		}
	}

	return app.ToolRunResult{Output: map[string]any{"status": "timeout", "branch": request.Branch}, ExitCode: 1}, nil
}

// ---------------------------------------------------------------------------
// github.merge_pr
// ---------------------------------------------------------------------------

type GitHubMergePRHandler struct {
	runner app.CommandRunner
}

func NewGitHubMergePRHandler(runner app.CommandRunner) *GitHubMergePRHandler {
	return &GitHubMergePRHandler{runner: runner}
}

func (h *GitHubMergePRHandler) Name() string { return "github.merge_pr" }

func (h *GitHubMergePRHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Branch       string `json:"branch"`
		Method       string `json:"method"`
		DeleteBranch bool   `json:"delete_branch"`
	}{Method: "squash", DeleteBranch: true}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.merge_pr args", Retryable: false}
	}
	if strings.TrimSpace(request.Branch) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "branch is required", Retryable: false}
	}

	cmdArgs := []string{"pr", "merge", request.Branch}
	switch request.Method {
	case "squash":
		cmdArgs = append(cmdArgs, "--squash")
	case "rebase":
		cmdArgs = append(cmdArgs, "--rebase")
	default:
		cmdArgs = append(cmdArgs, "--merge")
	}
	if request.DeleteBranch {
		cmdArgs = append(cmdArgs, "--delete-branch")
	}

	result, err := h.runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "gh",
		Args:     cmdArgs,
		MaxBytes: 64 * 1024,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("gh pr merge failed: %v", err), Retryable: true}
	}

	return app.ToolRunResult{
		Output:   map[string]any{"merged": true, "branch": request.Branch, "method": request.Method, "output": strings.TrimSpace(result.Output)},
		ExitCode: result.ExitCode,
	}, nil
}

// ---------------------------------------------------------------------------
// github.watch_run
// ---------------------------------------------------------------------------

type GitHubWatchRunHandler struct {
	runner app.CommandRunner
}

func NewGitHubWatchRunHandler(runner app.CommandRunner) *GitHubWatchRunHandler {
	return &GitHubWatchRunHandler{runner: runner}
}

func (h *GitHubWatchRunHandler) Name() string { return "github.watch_run" }

func (h *GitHubWatchRunHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Branch string `json:"branch"`
		RunID  int    `json:"run_id"`
	}{}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid github.watch_run args", Retryable: false}
	}
	if request.RunID == 0 && strings.TrimSpace(request.Branch) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "branch or run_id is required", Retryable: false}
	}

	runID := request.RunID

	// Resolve run ID from branch when not provided directly.
	// GitHub Actions may take a few seconds to register the run after a push,
	// so retry for up to 60 seconds.
	if runID == 0 {
		deadline := time.Now().Add(60 * time.Second)
		for runID == 0 && time.Now().Before(deadline) {
			listResult, listErr := h.runner.Run(ctx, session, app.CommandSpec{
				Cwd:      session.WorkspacePath,
				Command:  "gh",
				Args:     []string{"run", "list", "--branch", request.Branch, "--limit", "1", "--json", "databaseId", "-q", ".[0].databaseId"},
				MaxBytes: 4096,
			})
			if listErr == nil {
				if id, parseErr := strconv.Atoi(strings.TrimSpace(listResult.Output)); parseErr == nil {
					runID = id
					break
				}
			}
			select {
			case <-ctx.Done():
				return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeTimeout, Message: "context cancelled waiting for workflow run", Retryable: false}
			case <-time.After(5 * time.Second):
			}
		}
		if runID == 0 {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "no workflow run found for branch after 60s", Retryable: true}
		}
	}

	// Block until the run completes. --exit-status makes gh return non-zero
	// on failure, so the exit code alone tells us the outcome.
	result, err := h.runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "gh",
		Args:     []string{"run", "watch", strconv.Itoa(runID), "--exit-status"},
		MaxBytes: 64 * 1024,
	})

	status := "passed"
	exitCode := 0
	output := ""
	if err != nil {
		status = "failed"
		exitCode = 1
		output = err.Error()
	} else {
		output = strings.TrimSpace(result.Output)
		if result.ExitCode != 0 {
			status = "failed"
			exitCode = result.ExitCode
		}
	}

	return app.ToolRunResult{
		Output:   map[string]any{"status": status, "run_id": runID, "branch": request.Branch, "output": output},
		ExitCode: exitCode,
	}, nil
}
