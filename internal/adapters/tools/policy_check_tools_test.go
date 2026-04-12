package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestPolicyCheckHandler_AllowedTool(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	result, err := handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{
		"tool_name": "fs.read_file",
		"args":      map[string]any{"path": "src/main.go"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["allowed"] != true {
		t.Fatalf("expected allowed=true for valid fs.read_file, got: %v (reason: %v)", output["allowed"], output["reason"])
	}
}

func TestPolicyCheckHandler_PathEscape(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	result, err := handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{
		"tool_name": "fs.edit",
		"args":      map[string]any{"path": "../../../etc/passwd"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["allowed"] != false {
		t.Fatalf("expected allowed=false for path escape")
	}
}

func TestPolicyCheckHandler_ToolNotFound(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	result, err := handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{
		"tool_name": "nonexistent.tool",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["allowed"] != false {
		t.Fatalf("expected allowed=false for unknown tool")
	}
}

func TestPolicyCheckHandler_RiskLevel(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	result, err := handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{
		"tool_name": "shell.exec",
		"args":      map[string]any{"command": "make test"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["risk_level"] != "high" {
		t.Fatalf("expected risk_level=high for shell.exec, got: %v", output["risk_level"])
	}
	if output["requires_approval"] != true {
		t.Fatalf("expected requires_approval=true for shell.exec")
	}
}

func TestPolicyCheckHandler_NoArgs(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	result, err := handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{
		"tool_name": "git.status",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["allowed"] != true {
		t.Fatalf("expected allowed=true with no args")
	}
}

func TestPolicyCheckHandler_Validation(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	handler := NewPolicyCheckHandler()

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustPolicyJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected tool_name required, got %#v", err)
	}
}

func mustPolicyJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
