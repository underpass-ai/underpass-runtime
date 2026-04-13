package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestGitHubGetIssueHandler_Validation(t *testing.T) {
	handler := NewGitHubGetIssueHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustIssueJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected number required, got %#v", err)
	}
}

func TestGitHubGetIssueHandler_Success(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) < 3 || spec.Args[0] != "issue" || spec.Args[1] != "view" {
				return app.CommandResult{}, fmt.Errorf("unexpected: %v", spec.Args)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   `{"number":42,"title":"Fix auth bug","body":"Description","state":"OPEN"}`,
			}, nil
		},
	}
	handler := NewGitHubGetIssueHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, mustIssueJSON(t, map[string]any{"number": 42}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["title"] != "Fix auth bug" {
		t.Fatalf("expected title, got %v", output["title"])
	}
}

func TestGitHubGetIssueHandler_WithRepo(t *testing.T) {
	var capturedArgs []string
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			capturedArgs = spec.Args
			return app.CommandResult{ExitCode: 0, Output: `{"number":1}`}, nil
		},
	}
	handler := NewGitHubGetIssueHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, mustIssueJSON(t, map[string]any{
		"number": 1, "repo": "org/repo",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	found := false
	for i, arg := range capturedArgs {
		if arg == "--repo" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "org/repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --repo org/repo in args, got %v", capturedArgs)
	}
}

func mustIssueJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
