package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestGitBlameHandler_BasicBlame(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewGitBlameHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{
		"path": testGitMainTxt,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	blame := result.Output.(map[string]any)["blame"].(string)
	if !strings.Contains(blame, "line1") {
		t.Fatalf("expected blame to contain file content, got: %s", blame[:100])
	}
}

func TestGitBlameHandler_LineRange(t *testing.T) {
	root := initGitRepo(t)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewGitBlameHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{
		"path": testGitMainTxt, "start_line": 1, "end_line": 1,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	blame := result.Output.(map[string]any)["blame"].(string)
	lines := strings.Split(strings.TrimSpace(blame), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 blame line, got %d", len(lines))
	}
}

func TestGitBlameHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewGitBlameHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustJSONGit(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}
}

func TestGitBlameHandler_Name(t *testing.T) {
	if NewGitBlameHandler(nil).Name() != "git.blame" {
		t.Fatal("expected git.blame")
	}
}
