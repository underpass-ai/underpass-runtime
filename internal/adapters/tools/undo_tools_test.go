package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestWorkspaceUndoEdit_AfterEdit(t *testing.T) {
	root := t.TempDir()
	original := "original content"
	os.WriteFile(filepath.Join(root, "file.txt"), []byte(original), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	// Simulate an edit that saves undo snapshot.
	editHandler := NewFSEditHandler(nil)
	_, err := editHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt", "old_string": "original", "new_string": "modified",
	}))
	if err != nil {
		t.Fatalf("edit failed: %#v", err)
	}

	// Verify file was modified.
	data, _ := os.ReadFile(filepath.Join(root, "file.txt"))
	if string(data) != "modified content" {
		t.Fatalf("expected modified content, got: %q", string(data))
	}

	// Undo the edit.
	undoHandler := NewWorkspaceUndoEditHandler(nil)
	result, err := undoHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt",
	}))
	if err != nil {
		t.Fatalf("undo failed: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["restored"] != true {
		t.Fatalf("expected restored=true")
	}

	// Verify file was restored.
	data, _ = os.ReadFile(filepath.Join(root, "file.txt"))
	if string(data) != original {
		t.Fatalf("expected original content after undo, got: %q", string(data))
	}

	// Second undo should fail — snapshot was consumed.
	_, err = undoHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected no snapshot error on second undo, got %#v", err)
	}
}

func TestWorkspaceUndoEdit_AfterInsert(t *testing.T) {
	root := t.TempDir()
	original := "line1\nline2\n"
	os.WriteFile(filepath.Join(root, "file.txt"), []byte(original), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	insertHandler := NewFSInsertHandler(nil)
	_, err := insertHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt", "line": 1, "content": "inserted",
	}))
	if err != nil {
		t.Fatalf("insert failed: %#v", err)
	}

	undoHandler := NewWorkspaceUndoEditHandler(nil)
	_, err = undoHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt",
	}))
	if err != nil {
		t.Fatalf("undo failed: %#v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "file.txt"))
	if string(data) != original {
		t.Fatalf("expected original after undo, got: %q", string(data))
	}
}

func TestWorkspaceUndoEdit_NoSnapshot(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewWorkspaceUndoEditHandler(nil)

	_, err := handler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected no snapshot error, got %#v", err)
	}
}

func TestWorkspaceUndoEdit_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewWorkspaceUndoEditHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}
}

func mustUndoJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}

func TestWorkspaceUndoEdit_AfterWrite(t *testing.T) {
	root := t.TempDir()
	original := "original"
	os.WriteFile(filepath.Join(root, "file.txt"), []byte(original), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	writeHandler := NewFSWriteHandler(nil)
	_, err := writeHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{
		"path": "file.txt", "content": "overwritten",
	}))
	if err != nil {
		t.Fatalf("write failed: %#v", err)
	}

	undoHandler := NewWorkspaceUndoEditHandler(nil)
	_, err = undoHandler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{"path": "file.txt"}))
	if err != nil {
		t.Fatalf("undo failed: %#v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "file.txt"))
	if string(data) != original {
		t.Fatalf("expected original after undo, got: %q", string(data))
	}
}

func TestWorkspaceUndoEdit_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}

	callCount := 0
	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			callCount++
			if callCount == 1 {
				return app.CommandResult{ExitCode: 0, Output: "snapshot content"}, nil
			}
			return app.CommandResult{ExitCode: 0, Output: ""}, nil
		},
	}
	handler := NewWorkspaceUndoEditHandler(runner)
	result, err := handler.Invoke(context.Background(), session, mustUndoJSON(t, map[string]any{"path": "file.txt"}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	if result.Output.(map[string]any)["restored"] != true {
		t.Fatalf("expected restored=true")
	}
}
