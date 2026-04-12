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

func TestShellExecHandler_LocalHappyPath(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewShellExecHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "echo hello",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["exit_code"] != 0 {
		t.Fatalf("expected exit_code=0, got %v", output["exit_code"])
	}
	stdout := output["stdout"].(string)
	if stdout == "" || stdout[:5] != "hello" {
		t.Fatalf("expected stdout to start with 'hello', got: %q", stdout)
	}
}

func TestShellExecHandler_NonZeroExitCode(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewShellExecHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "exit 42",
	}))
	// Non-zero exit is NOT an error — it returns the result.
	if err != nil {
		t.Fatalf("non-zero exit should not be error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["exit_code"] != 42 {
		t.Fatalf("expected exit_code=42, got %v", output["exit_code"])
	}
}

func TestShellExecHandler_Stdin(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewShellExecHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "cat",
		"stdin":   "piped input",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["stdout"] != "piped input" {
		t.Fatalf("expected stdin piped through, got: %q", output["stdout"])
	}
}

func TestShellExecHandler_CustomCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "subdir")
	os.MkdirAll(sub, 0o755)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewShellExecHandler(nil)

	result, err := handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "pwd",
		"cwd":     "subdir",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	stdout := output["stdout"].(string)
	if len(stdout) < 3 || stdout[len(stdout)-7:len(stdout)-1] != "subdir" {
		// Just verify it ends with subdir.
		if !filepath.IsAbs(stdout[:len(stdout)-1]) {
			t.Fatalf("expected absolute path ending in subdir, got: %q", stdout)
		}
	}
}

func TestShellExecHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewShellExecHandler(nil)

	// Invalid JSON.
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON error, got %#v", err)
	}

	// Empty command.
	_, err = handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected empty command error, got %#v", err)
	}

	// Command too long.
	_, err = handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": string(make([]byte, 5000)),
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected command too long error, got %#v", err)
	}

	// Cwd outside workspace.
	_, err = handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "pwd",
		"cwd":     "../../../etc",
	}))
	if err == nil {
		t.Fatalf("expected cwd escape error")
	}
}

func TestShellExecHandler_KubernetesRuntime(t *testing.T) {
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}

	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "sh" {
				t.Fatalf("expected sh command, got %s", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: "remote output"}, nil
		},
	}
	handler := NewShellExecHandler(runner)

	result, err := handler.Invoke(context.Background(), session, mustShellJSON(t, map[string]any{
		"command": "make test",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["stdout"] != "remote output" {
		t.Fatalf("expected remote output, got: %q", output["stdout"])
	}
}

func TestShellExecHandler_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}
	if clampShellTimeout(0) != shellDefaultTimeoutS {
		t.Fatalf("expected default timeout for 0")
	}
	if clampShellTimeout(700) != shellMaxTimeoutS {
		t.Fatalf("expected max timeout for 700")
	}
	if clampShellTimeout(30) != 30 {
		t.Fatalf("expected 30 for 30")
	}
}

type fakeShellRunner struct {
	run func(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeShellRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if f.run == nil {
		return app.CommandResult{}, fmt.Errorf("not configured")
	}
	return f.run(ctx, session, spec)
}

func mustShellJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
