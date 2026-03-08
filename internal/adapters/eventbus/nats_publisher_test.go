package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeJSPublisher struct {
	mu       sync.Mutex
	messages []fakePublished
	err      error
}

type fakePublished struct {
	Subject string
	Data    []byte
}

func (f *fakeJSPublisher) Publish(_ context.Context, subject string, data []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, fakePublished{
		Subject: subject,
		Data:    data,
	})
	return &jetstream.PubAck{Stream: "test-stream", Sequence: uint64(len(f.messages))}, nil
}

func TestNATSPublisher_Publish(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := newNATSPublisherWithJS(fake, "ws.events", nil)

	evt, err := domain.NewDomainEvent("evt-nats-1", domain.EventSessionCreated, "sess-1", "t1", "a1", domain.SessionCreatedPayload{
		RuntimeKind: domain.RuntimeKindDocker,
		RepoURL:     "https://github.com/org/repo",
	})
	if err != nil {
		t.Fatalf("unexpected error creating event: %v", err)
	}

	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(fake.messages))
	}
	msg := fake.messages[0]
	expectedSubject := "ws.events." + string(domain.EventSessionCreated)
	if msg.Subject != expectedSubject {
		t.Fatalf("expected subject %q, got %q", expectedSubject, msg.Subject)
	}

	var decoded domain.DomainEvent
	if err := json.Unmarshal(msg.Data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal published data: %v", err)
	}
	if decoded.ID != "evt-nats-1" {
		t.Fatalf("expected event ID evt-nats-1, got %s", decoded.ID)
	}
	if decoded.Type != domain.EventSessionCreated {
		t.Fatalf("expected event type %s, got %s", domain.EventSessionCreated, decoded.Type)
	}
}

func TestNATSPublisher_Publish_DefaultPrefix(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := newNATSPublisherWithJS(fake, "", nil)

	evt, _ := domain.NewDomainEvent("evt-def", domain.EventInvocationStarted, "s", "t", "a", domain.InvocationStartedPayload{
		InvocationID: "inv-1",
		ToolName:     "fs.read",
	})

	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(fake.messages))
	}
	expected := defaultSubjectPrefix + "." + string(domain.EventInvocationStarted)
	if fake.messages[0].Subject != expected {
		t.Fatalf("expected subject %q, got %q", expected, fake.messages[0].Subject)
	}
}

func TestNATSPublisher_Publish_Error(t *testing.T) {
	fake := &fakeJSPublisher{err: fmt.Errorf("connection refused")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pub := newNATSPublisherWithJS(fake, "ws.events", logger)

	evt, _ := domain.NewDomainEvent("evt-err", domain.EventSessionClosed, "s", "t", "a", domain.SessionClosedPayload{
		DurationSec: 100,
	})

	err := pub.Publish(context.Background(), evt)
	if err == nil {
		t.Fatal("expected error from publish")
	}
	if buf.Len() == 0 {
		t.Fatal("expected warning log on publish error")
	}
}

func TestNATSPublisher_Publish_AllEventTypes(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := newNATSPublisherWithJS(fake, "test", nil)

	events := []struct {
		eventType domain.EventType
		payload   any
	}{
		{domain.EventSessionCreated, domain.SessionCreatedPayload{RuntimeKind: domain.RuntimeKindLocal}},
		{domain.EventSessionClosed, domain.SessionClosedPayload{DurationSec: 60}},
		{domain.EventInvocationStarted, domain.InvocationStartedPayload{InvocationID: "inv-1", ToolName: "fs.read"}},
		{domain.EventInvocationCompleted, domain.InvocationCompletedPayload{InvocationID: "inv-1", Status: domain.InvocationStatusSucceeded}},
		{domain.EventInvocationDenied, domain.InvocationDeniedPayload{InvocationID: "inv-2", Reason: "policy denied"}},
		{domain.EventArtifactStored, domain.ArtifactStoredPayload{ArtifactID: "art-1", SizeBytes: 1024}},
	}

	for _, e := range events {
		evt, err := domain.NewDomainEvent("evt-multi", e.eventType, "s", "t", "a", e.payload)
		if err != nil {
			t.Fatalf("error creating event %s: %v", e.eventType, err)
		}
		if err := pub.Publish(context.Background(), evt); err != nil {
			t.Fatalf("publish error for %s: %v", e.eventType, err)
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.messages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(fake.messages))
	}
}

func TestNATSPublisher_SubjectFormat(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := newNATSPublisherWithJS(fake, "workspace.events", nil)

	evt, _ := domain.NewDomainEvent("evt-sub", domain.EventArtifactStored, "s", "t", "a", domain.ArtifactStoredPayload{
		ArtifactID: "a1",
	})
	_ = pub.Publish(context.Background(), evt)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	expected := "workspace.events.workspace.artifact.stored"
	if fake.messages[0].Subject != expected {
		t.Fatalf("expected subject %q, got %q", expected, fake.messages[0].Subject)
	}
}

func TestNATSPublisher_DebugLog(t *testing.T) {
	fake := &fakeJSPublisher{}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pub := newNATSPublisherWithJS(fake, "ws", logger)

	evt, _ := domain.NewDomainEvent("evt-log", domain.EventSessionCreated, "s", "t", "a", domain.SessionCreatedPayload{})
	_ = pub.Publish(context.Background(), evt)

	var logged map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logged); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}
	if logged["event_id"] != "evt-log" {
		t.Fatalf("expected event_id in log, got %v", logged["event_id"])
	}
}
