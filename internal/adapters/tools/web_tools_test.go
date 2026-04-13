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
