package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestFSGlobHandler_LocalMatches(t *testing.T) {
	root := t.TempDir()
	// Create a directory structure.
	for _, path := range []string{
		"src/main.go",
		"src/handler.go",
		"src/handler_test.go",
		"src/pkg/util.go",
		"docs/README.md",
		"Makefile",
	} {
		full := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("// "+path), 0o644)
	}

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSGlobHandler(nil)

	// Match all Go files.
	result, err := handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "**/*.go",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	matches := toStringSlice(output["matches"])
	if len(matches) != 4 {
		t.Fatalf("expected 4 Go files, got %d: %v", len(matches), matches)
	}

	// Match only test files.
	result, err = handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "**/*_test.go",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output = result.Output.(map[string]any)
	matches = toStringSlice(output["matches"])
	if len(matches) != 1 {
		t.Fatalf("expected 1 test file, got %d: %v", len(matches), matches)
	}

	// Match in subdirectory.
	result, err = handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "*.go",
		"path":    "src/pkg",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output = result.Output.(map[string]any)
	matches = toStringSlice(output["matches"])
	if len(matches) != 1 || matches[0] != "src/pkg/util.go" {
		t.Fatalf("expected src/pkg/util.go, got: %v", matches)
	}
}

func TestFSGlobHandler_Truncation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%d.txt", i)), []byte("x"), 0o644)
	}

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSGlobHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern":   "*.txt",
		"max_files": 3,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", output["truncated"])
	}
	matches := toStringSlice(output["matches"])
	if len(matches) > 3 {
		t.Fatalf("expected at most 3 matches, got %d", len(matches))
	}
}

func TestFSGlobHandler_NoMatches(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewFSGlobHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "**/*.xyz",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 0 {
		t.Fatalf("expected 0 matches, got %v", output["count"])
	}
}

func TestFSGlobHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewFSGlobHandler(nil)

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Empty pattern.
	_, err = handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected pattern required error, got %#v", err)
	}

	// Invalid pattern.
	_, err = handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "[invalid",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid pattern error, got %#v", err)
	}

	// Path escape.
	_, err = handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "*.go",
		"path":    "../../../etc",
	}))
	if err == nil {
		t.Fatalf("expected path escape error")
	}
}

func TestFSGlobHandler_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}

	runner := &fakeGlobRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{
				ExitCode: 0,
				Output:   "./src/main.go\n./src/util.go\n./docs/README.md\n",
			}, nil
		},
	}
	handler := NewFSGlobHandler(runner)

	result, err := handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "**/*.go",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	matches := toStringSlice(output["matches"])
	if len(matches) != 2 {
		t.Fatalf("expected 2 Go matches from remote, got %d: %v", len(matches), matches)
	}
}

func TestFSGlobHandler_KubernetesRunnerError(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}

	// Nil runner.
	handler := NewFSGlobHandler(nil)
	_, err := handler.Invoke(context.Background(), session, mustGlobJSON(t, map[string]any{
		"pattern": "*.go",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected nil runner error, got %#v", err)
	}
}

type fakeGlobRunner struct {
	run func(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeGlobRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if f.run == nil {
		return app.CommandResult{}, fmt.Errorf("not configured")
	}
	return f.run(ctx, session, spec)
}

func mustGlobJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func toStringSlice(v any) []string {
	if ss, ok := v.([]string); ok {
		return ss
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
