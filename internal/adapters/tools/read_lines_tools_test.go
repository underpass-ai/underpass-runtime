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

func TestFSReadLinesHandler_ReadRange(t *testing.T) {
	root := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	os.WriteFile(filepath.Join(root, "test.txt"), []byte(content), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSReadLinesHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{
		"path": "test.txt", "start_line": 3, "end_line": 5,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	c := output["content"].(string)
	if !strings.Contains(c, "3\tline3") {
		t.Fatalf("expected line 3, got: %s", c)
	}
	if !strings.Contains(c, "5\tline5") {
		t.Fatalf("expected line 5, got: %s", c)
	}
	if strings.Contains(c, "line6") {
		t.Fatalf("should not contain line 6")
	}
	if output["total_lines"] != 10 {
		t.Fatalf("expected 10 total lines, got %v", output["total_lines"])
	}
}

func TestFSReadLinesHandler_ClampRange(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "small.txt"), []byte("a\nb\n"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSReadLinesHandler(nil)

	// start_line > total lines — empty content, no error.
	result, err := handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{
		"path": "small.txt", "start_line": 100, "end_line": 200,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["content"] != "" {
		t.Fatalf("expected empty content, got: %q", output["content"])
	}
}

func TestFSReadLinesHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewFSReadLinesHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{"path": "nonexistent.txt"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected not found, got %#v", err)
	}
}

func mustReadLinesJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}

func TestFSReadLinesHandler_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	handler := NewFSReadLinesHandler(&fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "3  line3\n4  line4\n5  line5\n---TOTAL---\n10"}, nil
		},
	})

	result, err := handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{
		"path": "test.txt", "start_line": 3, "end_line": 5,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["total_lines"] != 10 {
		t.Fatalf("expected 10 total lines, got %v", output["total_lines"])
	}
}

func TestFSReadLinesHandler_NilRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	handler := NewFSReadLinesHandler(nil)
	_, err := handler.Invoke(context.Background(), session, mustReadLinesJSON(t, map[string]any{
		"path": "test.txt",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected nil runner error, got %#v", err)
	}
}
