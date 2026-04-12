package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestFSInsertHandler_InsertMiddle(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.go"), []byte("line1\nline2\nline3\n"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSInsertHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"path": "test.go", "line": 2, "content": "inserted",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["inserted_at"] != 2 {
		t.Fatalf("expected inserted_at=2, got %v", output["inserted_at"])
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.go"))
	lines := strings.Split(string(data), "\n")
	if lines[2] != "inserted" {
		t.Fatalf("expected 'inserted' at line 3 (index 2), got: %v", lines)
	}
}

func TestFSInsertHandler_Prepend(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.go"), []byte("line1\nline2\n"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSInsertHandler(nil)

	_, err := handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"path": "test.go", "line": 0, "content": "header",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "test.go"))
	if !strings.HasPrefix(string(data), "header\n") {
		t.Fatalf("expected header prepended, got: %q", string(data))
	}
}

func TestFSInsertHandler_Append(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.go"), []byte("line1\nline2"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSInsertHandler(nil)

	_, err := handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"path": "test.go", "line": 999, "content": "footer",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "test.go"))
	if !strings.Contains(string(data), "footer") {
		t.Fatalf("expected footer appended, got: %q", string(data))
	}
}

func TestFSInsertHandler_Validation(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "exists.txt"), []byte("x"), 0o644)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSInsertHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"line": 1, "content": "x",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"path": "exists.txt", "line": 1,
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected content required, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustInsertJSON(t, map[string]any{
		"path": "missing.txt", "line": 1, "content": "x",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected not found, got %#v", err)
	}
}

func mustInsertJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
