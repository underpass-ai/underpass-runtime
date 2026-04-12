package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeGHRunner struct {
	calls []app.CommandSpec
	run   func(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeGHRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(ctx, session, spec)
	}
	return app.CommandResult{}, fmt.Errorf("not configured")
}

func ghJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestGitHubCreatePRHandler_Validation(t *testing.T) {
	runner := &fakeGHRunner{}
	handler := NewGitHubCreatePRHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Missing title.
	_, err = handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{"head": "feat/x"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected title required, got %#v", err)
	}

	// Missing head.
	_, err = handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{"title": "Fix"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected head required, got %#v", err)
	}
}

func TestGitHubCreatePRHandler_Success(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{Output: "https://github.com/org/repo/pull/1\n", ExitCode: 0}, nil
		},
	}
	handler := NewGitHubCreatePRHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"title": "Fix bug", "head": "feat/fix", "base": "main",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pr_url"] != "https://github.com/org/repo/pull/1" {
		t.Fatalf("expected PR URL, got %v", output["pr_url"])
	}
}

func TestGitHubCreatePRHandler_RunnerError(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{}, fmt.Errorf("gh not found")
		},
	}
	handler := NewGitHubCreatePRHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"title": "Fix", "head": "feat/fix",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution error, got %#v", err)
	}
}

func TestGitHubMergePRHandler_Validation(t *testing.T) {
	runner := &fakeGHRunner{}
	handler := NewGitHubMergePRHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Missing branch.
	_, err = handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected branch required, got %#v", err)
	}
}

func TestGitHubMergePRHandler_Methods(t *testing.T) {
	for _, method := range []string{"squash", "rebase", "merge"} {
		t.Run(method, func(t *testing.T) {
			runner := &fakeGHRunner{
				run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
					return app.CommandResult{Output: "merged\n", ExitCode: 0}, nil
				},
			}
			handler := NewGitHubMergePRHandler(runner)
			session := domain.Session{WorkspacePath: t.TempDir()}

			result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
				"branch": "feat/fix", "method": method,
			}))
			if err != nil {
				t.Fatalf("unexpected error for method %s: %#v", method, err)
			}
			output := result.Output.(map[string]any)
			if output["merged"] != true {
				t.Fatalf("expected merged=true")
			}
		})
	}
}

func TestGitHubMergePRHandler_RunnerError(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{}, fmt.Errorf("gh failed")
		},
	}
	handler := NewGitHubMergePRHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"branch": "feat/fix",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution error, got %#v", err)
	}
}

func TestGitHubWatchRunHandler_Validation(t *testing.T) {
	runner := &fakeGHRunner{}
	handler := NewGitHubWatchRunHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Missing both branch and run_id.
	_, err = handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected branch or run_id required, got %#v", err)
	}
}

func TestGitHubWatchRunHandler_WithRunID(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			// gh run watch <id> --exit-status
			if len(spec.Args) >= 3 && spec.Args[0] == "run" && spec.Args[1] == "watch" {
				return app.CommandResult{Output: "completed\n", ExitCode: 0}, nil
			}
			return app.CommandResult{}, fmt.Errorf("unexpected command: %v", spec.Args)
		},
	}
	handler := NewGitHubWatchRunHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"run_id": 12345,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "passed" {
		t.Fatalf("expected status=passed, got %v", output["status"])
	}
}

func TestGitHubWatchRunHandler_RunFailed(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) >= 3 && spec.Args[1] == "watch" {
				return app.CommandResult{Output: "failed\n", ExitCode: 1}, fmt.Errorf("exit status 1")
			}
			return app.CommandResult{}, fmt.Errorf("unexpected")
		},
	}
	handler := NewGitHubWatchRunHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"run_id": 99,
	}))
	if err != nil {
		t.Fatalf("unexpected error (run failure should return result, not error): %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "failed" {
		t.Fatalf("expected status=failed, got %v", output["status"])
	}
}

func TestGitHubCheckPRStatusHandler_Validation(t *testing.T) {
	runner := &fakeGHRunner{}
	handler := NewGitHubCheckPRStatusHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Missing branch.
	_, err = handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected branch required, got %#v", err)
	}
}

func TestGitHubCheckPRStatusHandler_AllPassed(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{
				Output:   `[{"name":"Lint","state":"COMPLETED","conclusion":"SUCCESS"},{"name":"Test","state":"COMPLETED","conclusion":"SUCCESS"}]`,
				ExitCode: 0,
			}, nil
		},
	}
	handler := NewGitHubCheckPRStatusHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"branch": "feat/x", "timeout_seconds": 1,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "passed" {
		t.Fatalf("expected status=passed, got %v", output["status"])
	}
}

func TestGitHubCheckPRStatusHandler_Failed(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{
				Output:   `[{"name":"Lint","state":"COMPLETED","conclusion":"FAILURE"}]`,
				ExitCode: 0,
			}, nil
		},
	}
	handler := NewGitHubCheckPRStatusHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, ghJSON(t, map[string]any{
		"branch": "feat/x", "timeout_seconds": 1,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "failed" {
		t.Fatalf("expected status=failed, got %v", output["status"])
	}
}

func TestGitHubHandlerNames(t *testing.T) {
	runner := &fakeGHRunner{}
	tests := []struct {
		handler Handler
		name    string
	}{
		{NewGitHubCreatePRHandler(runner), "github.create_pr"},
		{NewGitHubCheckPRStatusHandler(runner), "github.check_pr_status"},
		{NewGitHubMergePRHandler(runner), "github.merge_pr"},
		{NewGitHubWatchRunHandler(runner), "github.watch_run"},
	}
	for _, tt := range tests {
		if tt.handler.Name() != tt.name {
			t.Fatalf("expected name %q, got %q", tt.name, tt.handler.Name())
		}
	}
}
