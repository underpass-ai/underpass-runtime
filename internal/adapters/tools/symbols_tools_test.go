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

func TestRepoSymbolsHandler_GoFile(t *testing.T) {
	root := t.TempDir()
	goCode := `package main

import "fmt"

const Version = "1.0"

type Service struct {
	Name string
}

func (s *Service) Start() error {
	return nil
}

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(filepath.Join(root, "main.go"), []byte(goCode), 0o644)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewRepoSymbolsHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustSymbolsJSON(t, map[string]any{"path": "main.go"}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["language"] != "go" {
		t.Fatalf("expected go, got %v", output["language"])
	}
	symbols := output["symbols"].([]sourceSymbol)
	if len(symbols) < 4 {
		t.Fatalf("expected >= 4 symbols (package, import, type, func), got %d: %+v", len(symbols), symbols)
	}

	kinds := map[string]bool{}
	for _, s := range symbols {
		kinds[s.Kind] = true
	}
	for _, expected := range []string{"package", "type", "function"} {
		if !kinds[expected] {
			t.Fatalf("expected %s symbol, got kinds: %v", expected, kinds)
		}
	}
}

func TestRepoSymbolsHandler_PythonFile(t *testing.T) {
	root := t.TempDir()
	pyCode := `import os
from pathlib import Path

class MyService:
    def __init__(self):
        pass

    def start(self):
        pass

def main():
    svc = MyService()

async def process():
    pass
`
	os.WriteFile(filepath.Join(root, "app.py"), []byte(pyCode), 0o644)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewRepoSymbolsHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustSymbolsJSON(t, map[string]any{"path": "app.py"}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["language"] != "python" {
		t.Fatalf("expected python, got %v", output["language"])
	}
	count := output["count"].(int)
	if count < 4 {
		t.Fatalf("expected >= 4 symbols, got %d", count)
	}
}

func TestRepoSymbolsHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewRepoSymbolsHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustSymbolsJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustSymbolsJSON(t, map[string]any{"path": "missing.go"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected not found, got %#v", err)
	}
}

func TestRepoSymbolsHandler_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "package main\n\nfunc main() {}\n"}, nil
		},
	}
	handler := NewRepoSymbolsHandler(runner)
	result, err := handler.Invoke(context.Background(), session, mustSymbolsJSON(t, map[string]any{"path": "main.go"}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	count := output["count"].(int)
	if count < 2 {
		t.Fatalf("expected >= 2 symbols, got %d", count)
	}
}

func mustSymbolsJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
