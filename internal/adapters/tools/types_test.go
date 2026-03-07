package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type stubHandler struct {
	name   string
	result app.ToolRunResult
	err    *domain.Error
}

func (s *stubHandler) Name() string {
	return s.name
}

func (s *stubHandler) Invoke(_ context.Context, _ domain.Session, _ json.RawMessage) (app.ToolRunResult, *domain.Error) {
	return s.result, s.err
}

func TestEngineInvokeAndNotFound(t *testing.T) {
	handler := &stubHandler{name: "demo.tool", result: app.ToolRunResult{Output: map[string]any{"ok": true}}}
	engine := NewEngine(handler)
	session := domain.Session{}

	result, err := engine.Invoke(context.Background(), session, domain.Capability{Name: "demo.tool"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected invoke error: %v", err)
	}
	if value, ok := result.Output.(map[string]any)["ok"].(bool); !ok || !value {
		t.Fatalf("unexpected output: %#v", result.Output)
	}

	_, err = engine.Invoke(context.Background(), session, domain.Capability{Name: "missing.tool"}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected not found error")
	}
	if err.Code != app.ErrorCodeNotFound {
		t.Fatalf("unexpected code: %s", err.Code)
	}
}

func TestCatalogGetAndList(t *testing.T) {
	catalog := NewCatalog([]domain.Capability{{Name: "a"}, {Name: "b"}})
	if _, found := catalog.Get("a"); !found {
		t.Fatal("expected capability a")
	}
	if _, found := catalog.Get("missing"); found {
		t.Fatal("did not expect missing capability")
	}
	if len(catalog.List()) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(catalog.List()))
	}
}
