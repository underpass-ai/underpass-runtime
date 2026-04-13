package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestRepoTestFileHandler_Validation(t *testing.T) {
	handler := NewRepoTestFileHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir()}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{bad`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid JSON, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustTestFileJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path required, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustTestFileJSON(t, map[string]any{"path": "test.unknown"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected unsupported type, got %#v", err)
	}
}

func TestRepoTestFileHandler_GoTest(t *testing.T) {
	runner := &fakeShellRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "ok\tmy/pkg\t0.5s\n"}, nil
		},
	}
	handler := NewRepoTestFileHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir()}

	result, err := handler.Invoke(context.Background(), session, mustTestFileJSON(t, map[string]any{
		"path": "internal/app/service_test.go",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["passed"] != true {
		t.Fatalf("expected passed=true, got %v", output["passed"])
	}
}

func TestRepoTestFileHandler_Name(t *testing.T) {
	if NewRepoTestFileHandler(nil).Name() != "repo.test_file" {
		t.Fatal("expected repo.test_file")
	}
}

func mustTestFileJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, _ := json.Marshal(v)
	return data
}
