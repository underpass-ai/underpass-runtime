package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestGitHubListIssuesHandler_Validation(t *testing.T) {
	handler := NewGitHubListIssuesHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}
}

func TestGitHubListIssuesHandler_Success(t *testing.T) {
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Args[0] != "issue" || spec.Args[1] != "list" {
				return app.CommandResult{}, fmt.Errorf("unexpected: %v", spec.Args)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   `[{"number":1,"title":"Bug"},{"number":2,"title":"Feature"}]`,
			}, nil
		},
	}
	handler := NewGitHubListIssuesHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, mustIssueJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 2 {
		t.Fatalf("expected 2 issues, got %v", output["count"])
	}
}

func TestGitHubListIssuesHandler_WithFilters(t *testing.T) {
	var capturedArgs []string
	runner := &fakeGHRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			capturedArgs = spec.Args
			return app.CommandResult{ExitCode: 0, Output: `[]`}, nil
		},
	}
	handler := NewGitHubListIssuesHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, mustIssueJSON(t, map[string]any{
		"state": "closed", "labels": "bug", "repo": "org/repo", "limit": 3,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	args := fmt.Sprintf("%v", capturedArgs)
	for _, expected := range []string{"--state", "closed", "--label", "bug", "--repo", "org/repo"} {
		if !containsSubstr(args, expected) {
			t.Fatalf("expected %s in args, got %v", expected, capturedArgs)
		}
	}
}

func TestGitHubListIssuesHandler_Name(t *testing.T) {
	if NewGitHubListIssuesHandler(nil).Name() != "github.list_issues" {
		t.Fatal("expected github.list_issues")
	}
}

func containsSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}
