package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestToolSuggestHandler_BasicSuggestion(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewToolSuggestHandler()

	result, err := handler.Invoke(context.Background(), session, mustSuggestJSON(t, map[string]any{
		"task": "edit a specific line in a Go file",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	suggestions := output["suggestions"].([]toolSuggestion)
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	// fs.edit should be highly ranked for "edit" task.
	found := false
	for _, s := range suggestions {
		if s.Name == "fs.edit" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected fs.edit in suggestions, got: %v", suggestions)
	}
}

func TestToolSuggestHandler_FamilyFilter(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewToolSuggestHandler()

	result, err := handler.Invoke(context.Background(), session, mustSuggestJSON(t, map[string]any{
		"task":   "find files",
		"family": "fs",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	suggestions := output["suggestions"].([]toolSuggestion)
	for _, s := range suggestions {
		if s.Name[:3] != "fs." {
			t.Fatalf("expected only fs.* tools with family filter, got: %s", s.Name)
		}
	}
}

func TestToolSuggestHandler_TopK(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewToolSuggestHandler()

	result, err := handler.Invoke(context.Background(), session, mustSuggestJSON(t, map[string]any{
		"task":  "do something with files",
		"top_k": 2,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	suggestions := output["suggestions"].([]toolSuggestion)
	if len(suggestions) > 2 {
		t.Fatalf("expected at most 2 suggestions, got %d", len(suggestions))
	}
}

func TestToolSuggestHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewToolSuggestHandler()

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustSuggestJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected task required error, got %#v", err)
	}
}

func TestToolSuggestHandler_ShellSuggestion(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewToolSuggestHandler()

	result, err := handler.Invoke(context.Background(), session, mustSuggestJSON(t, map[string]any{
		"task": "run make build command",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	suggestions := result.Output.(map[string]any)["suggestions"].([]toolSuggestion)
	found := false
	for _, s := range suggestions {
		if s.Name == "shell.exec" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected shell.exec for 'run make build', got: %v", suggestions)
	}
}

func mustSuggestJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
