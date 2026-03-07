package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInvokeToolEmitsOtelSpan(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider()
	tracerProvider.RegisterSpanProcessor(spanRecorder)

	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
	})

	session := defaultSession()
	capability := defaultCapability()

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}}},
		&fakeArtifactStore{},
	)

	_, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{
		Args:     json.RawMessage(`{}`),
		Approved: true,
	})
	if err != nil {
		t.Fatalf("expected successful invocation, got %#v", err)
	}

	ended := spanRecorder.Ended()
	if len(ended) == 0 {
		t.Fatal("expected at least one ended span")
	}

	var matched bool
	for _, span := range ended {
		if span.Name() != capability.Observability.SpanName {
			continue
		}
		matched = true
		if span.Status().Code != codes.Ok {
			t.Fatalf("expected span status code OK, got %v", span.Status().Code)
		}
		if !spanHasAttribute(span.Attributes(), "workspace.tool", capability.Name) {
			t.Fatalf("expected span attribute workspace.tool=%s", capability.Name)
		}
		if !spanHasAttribute(span.Attributes(), "workspace.invocation_status", "succeeded") {
			t.Fatalf("expected span attribute workspace.invocation_status=succeeded")
		}
	}
	if !matched {
		t.Fatalf("expected span %q not found", capability.Observability.SpanName)
	}
}

func spanHasAttribute(attrs []attribute.KeyValue, key string, expected string) bool {
	for _, attr := range attrs {
		if string(attr.Key) != key {
			continue
		}
		if attr.Value.Type() != attribute.STRING {
			continue
		}
		if attr.Value.AsString() == expected {
			return true
		}
	}
	return false
}
