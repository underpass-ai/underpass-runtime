package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestNoopPublisher_Publish(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pub := NewNoopPublisher(logger)

	evt, err := domain.NewDomainEvent("evt-1", domain.EventSessionCreated, "sess-1", "t1", "a1", domain.SessionCreatedPayload{
		RuntimeKind: domain.RuntimeKindDocker,
	})
	if err != nil {
		t.Fatalf("unexpected error creating event: %v", err)
	}

	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var logged map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logged); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}
	if logged["event_id"] != "evt-1" {
		t.Fatalf("expected event_id evt-1, got %v", logged["event_id"])
	}
	if logged["event_type"] != string(domain.EventSessionCreated) {
		t.Fatalf("expected event_type %s, got %v", domain.EventSessionCreated, logged["event_type"])
	}
	if logged["session_id"] != "sess-1" {
		t.Fatalf("expected session_id sess-1, got %v", logged["session_id"])
	}
}

func TestNoopPublisher_NilLogger(t *testing.T) {
	pub := NewNoopPublisher(nil)
	evt, _ := domain.NewDomainEvent("evt-2", domain.EventInvocationStarted, "s", "t", "a", domain.InvocationStartedPayload{
		InvocationID: "inv-1",
		ToolName:     "fs.read",
	})
	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNoopPublisher_MultipleEvents(t *testing.T) {
	pub := NewNoopPublisher(nil)

	types := []domain.EventType{
		domain.EventSessionCreated,
		domain.EventSessionClosed,
		domain.EventInvocationStarted,
		domain.EventInvocationCompleted,
		domain.EventInvocationDenied,
		domain.EventArtifactStored,
	}

	for _, et := range types {
		evt, _ := domain.NewDomainEvent("evt-multi", et, "s", "t", "a", map[string]string{"test": "value"})
		if err := pub.Publish(context.Background(), evt); err != nil {
			t.Fatalf("unexpected error for event type %s: %v", et, err)
		}
	}
}
