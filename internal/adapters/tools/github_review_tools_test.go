package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestGitHubReviewCommentsHandler_Validation(t *testing.T) {
	handler := NewGitHubReviewCommentsHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustReviewJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected branch required, got %#v", err)
	}
}

func TestGitHubReviewCommentsHandler_Success(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Args[0] != "pr" || spec.Args[1] != "view" {
				return app.CommandResult{}, fmt.Errorf("unexpected: %v", spec.Args)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   `{"reviews":[{"body":"LGTM"}],"comments":[],"reviewDecision":"APPROVED"}`,
			}, nil
		},
	}
	handler := NewGitHubReviewCommentsHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, mustReviewJSON(t, map[string]any{
		"branch": "feat/my-pr",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["reviewDecision"] != "APPROVED" {
		t.Fatalf("expected APPROVED, got %v", output["reviewDecision"])
	}
}

func TestGitHubReviewCommentsHandler_WithRepo(t *testing.T) {
	var capturedArgs []string
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			capturedArgs = spec.Args
			return app.CommandResult{ExitCode: 0, Output: `{}`}, nil
		},
	}
	handler := NewGitHubReviewCommentsHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, _ = handler.Invoke(context.Background(), session, mustReviewJSON(t, map[string]any{
		"branch": "feat/x", "repo": "org/repo",
	}))
	found := false
	for i, arg := range capturedArgs {
		if arg == "--repo" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "org/repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --repo in args, got %v", capturedArgs)
	}
}

func TestGitHubReviewCommentsHandler_Name(t *testing.T) {
	if NewGitHubReviewCommentsHandler(nil).Name() != "github.review_comments" {
		t.Fatal("expected github.review_comments")
	}
}

func mustReviewJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
