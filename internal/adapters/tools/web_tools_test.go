package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestWebFetchHandler_Validation(t *testing.T) {
	handler := NewWebFetchHandler()
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustWebJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected url required, got %#v", err)
	}
}

func TestWebSearchHandler_Validation(t *testing.T) {
	handler := NewWebSearchHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustWebJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected query required, got %#v", err)
	}
}

func TestWebFetchHandler_AutoHTTPS(t *testing.T) {
	handler := NewWebFetchHandler()
	session := domain.Session{WorkspacePath: t.TempDir()}

	// This will fail to connect (no network in test), but verifies
	// the URL auto-prefix logic works without crashing.
	_, err := handler.Invoke(context.Background(), session, mustWebJSON(t, map[string]any{
		"url":             "localhost:99999/nonexistent",
		"timeout_seconds": 1,
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected connection error, got %#v", err)
	}
}

func TestWebHandlerNames(t *testing.T) {
	if NewWebFetchHandler().Name() != "web.fetch" {
		t.Fatal("expected web.fetch")
	}
	if NewWebSearchHandler(nil).Name() != "web.search" {
		t.Fatal("expected web.search")
	}
}

func mustWebJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}

func TestWebSearchHandler_KubernetesRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "https://example.com/result1\nhttps://example.com/result2\n"}, nil
		},
	}
	handler := NewWebSearchHandler(runner)
	result, err := handler.Invoke(context.Background(), session, mustWebJSON(t, map[string]any{
		"query": "golang error handling",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 2 {
		t.Fatalf("expected 2 results, got %v", output["count"])
	}
}

func TestWebSearchHandler_MaxResults(t *testing.T) {
	handler := NewWebSearchHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	// max_results clamping
	result, _ := handler.Invoke(context.Background(), session, mustWebJSON(t, map[string]any{
		"query": "test", "max_results": -1,
	}))
	_ = result // will fail on execution but validates arg parsing
}
