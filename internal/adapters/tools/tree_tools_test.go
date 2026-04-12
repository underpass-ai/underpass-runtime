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

func TestRepoTreeHandler_BasicTree(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"src/main.go", "src/pkg/util.go", "docs/README.md"} {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("//"), 0o644)
	}

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewRepoTreeHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	tree := output["tree"].(string)
	if !strings.Contains(tree, "src/") {
		t.Fatalf("expected src/ in tree, got: %s", tree)
	}
	if !strings.Contains(tree, "docs/") {
		t.Fatalf("expected docs/ in tree, got: %s", tree)
	}
	if !strings.Contains(tree, "main.go") {
		t.Fatalf("expected main.go in tree, got: %s", tree)
	}
}

func TestRepoTreeHandler_DirsOnly(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src/pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "src/main.go"), []byte("//"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewRepoTreeHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{
		"show_files": false,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	tree := output["tree"].(string)
	if strings.Contains(tree, "main.go") {
		t.Fatalf("should not contain files when show_files=false, got: %s", tree)
	}
	if !strings.Contains(tree, "src/") {
		t.Fatalf("expected src/ directory, got: %s", tree)
	}
}

func TestRepoTreeHandler_IgnorePattern(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "node_modules/pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "src/main.go"), []byte("//"), 0o644)
	os.WriteFile(filepath.Join(root, "node_modules/pkg/index.js"), []byte("//"), 0o644)

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewRepoTreeHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	tree := result.Output.(map[string]any)["tree"].(string)
	if strings.Contains(tree, "node_modules") {
		t.Fatalf("node_modules should be ignored by default, got: %s", tree)
	}
}

func TestRepoTreeHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewRepoTreeHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{"path": "../../etc"}))
	if err == nil {
		t.Fatalf("expected path escape error")
	}
}

func mustTreeJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}

func TestRepoTreeHandler_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "./src\n./src/main.go\n./docs\n"}, nil
		},
	}
	handler := NewRepoTreeHandler(runner)
	result, err := handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["entries"] != 3 {
		t.Fatalf("expected 3 entries, got %v", output["entries"])
	}
}

func TestRepoTreeHandler_NilRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	handler := NewRepoTreeHandler(nil)
	_, err := handler.Invoke(context.Background(), session, mustTreeJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected nil runner error, got %#v", err)
	}
}
