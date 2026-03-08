package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// fakeJSPublisher records published messages and optionally returns errors.
type fakeJSPublisher struct {
	mu       sync.Mutex
	messages []fakeJSMessage
	err      error
}

type fakeJSMessage struct {
	Subject string
	Data    []byte
	MsgID   string
}

func (f *fakeJSPublisher) Publish(_ context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}

	// Extract MsgID from opts by inspecting the data directly —
	// we parse the Nats-Msg-Id from the event payload's ID field.
	var evt domain.DomainEvent
	msgID := ""
	if err := json.Unmarshal(data, &evt); err == nil {
		msgID = evt.ID
	}

	f.messages = append(f.messages, fakeJSMessage{
		Subject: subject,
		Data:    data,
		MsgID:   msgID,
	})
	return &jetstream.PubAck{Stream: "WORKSPACE_EVENTS", Sequence: uint64(len(f.messages))}, nil
}

func (f *fakeJSPublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *fakeJSPublisher) last() fakeJSMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages[len(f.messages)-1]
}

func makeNATSTestEvent(t *testing.T, id string, eventType domain.EventType) domain.DomainEvent {
	t.Helper()
	evt, err := domain.NewDomainEvent(id, eventType, "sess-1", "t1", "a1", domain.SessionCreatedPayload{
		RuntimeKind: domain.RuntimeKindDocker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return evt
}

func TestNATSPublisher_Publish(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := NewNATSPublisher(fake, "")

	evt := makeNATSTestEvent(t, "evt-001", domain.EventSessionCreated)
	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fake.count() != 1 {
		t.Fatalf("expected 1 message, got %d", fake.count())
	}
	msg := fake.last()
	if msg.Subject != "workspace.events.session.created" {
		t.Fatalf("expected subject workspace.events.session.created, got %s", msg.Subject)
	}
	if msg.MsgID != "evt-001" {
		t.Fatalf("expected msg_id evt-001, got %s", msg.MsgID)
	}
}

func TestNATSPublisher_SubjectMapping(t *testing.T) {
	tests := []struct {
		eventType domain.EventType
		expected  string
	}{
		{domain.EventSessionCreated, "workspace.events.session.created"},
		{domain.EventSessionClosed, "workspace.events.session.closed"},
		{domain.EventInvocationStarted, "workspace.events.invocation.started"},
		{domain.EventInvocationCompleted, "workspace.events.invocation.completed"},
		{domain.EventInvocationDenied, "workspace.events.invocation.denied"},
		{domain.EventArtifactStored, "workspace.events.artifact.stored"},
	}

	for _, tt := range tests {
		fake := &fakeJSPublisher{}
		pub := NewNATSPublisher(fake, "workspace.events")
		evt := makeNATSTestEvent(t, "evt-subj", tt.eventType)

		if err := pub.Publish(context.Background(), evt); err != nil {
			t.Fatalf("unexpected error for %s: %v", tt.eventType, err)
		}
		if fake.last().Subject != tt.expected {
			t.Fatalf("event %s: expected subject %s, got %s", tt.eventType, tt.expected, fake.last().Subject)
		}
	}
}

func TestNATSPublisher_CustomPrefix(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := NewNATSPublisher(fake, "myapp.events")

	evt := makeNATSTestEvent(t, "evt-cp", domain.EventSessionCreated)
	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fake.last().Subject != "myapp.events.session.created" {
		t.Fatalf("expected myapp.events.session.created, got %s", fake.last().Subject)
	}
}

func TestNATSPublisher_PublishError(t *testing.T) {
	fake := &fakeJSPublisher{err: errors.New("nats unavailable")}
	pub := NewNATSPublisher(fake, "")

	evt := makeNATSTestEvent(t, "evt-err", domain.EventSessionCreated)
	err := pub.Publish(context.Background(), evt)
	if err == nil {
		t.Fatal("expected publish error")
	}
	if !errors.Is(err, fake.err) {
		t.Fatalf("expected wrapped nats error, got %v", err)
	}
}

func TestNATSPublisher_DefaultPrefix(t *testing.T) {
	pub := NewNATSPublisher(&fakeJSPublisher{}, "")
	if pub.subjectPrefix != "workspace.events" {
		t.Fatalf("expected default prefix workspace.events, got %s", pub.subjectPrefix)
	}
}

func TestNATSPublisher_WhitespacePrefix(t *testing.T) {
	pub := NewNATSPublisher(&fakeJSPublisher{}, "   ")
	if pub.subjectPrefix != "workspace.events" {
		t.Fatalf("expected default prefix for whitespace, got %s", pub.subjectPrefix)
	}
}

func TestNATSPublisher_EventPayloadIntegrity(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := NewNATSPublisher(fake, "")

	payload := domain.InvocationCompletedPayload{
		InvocationID:  "inv-123",
		ToolName:      "fs.read",
		Status:        domain.InvocationStatusSucceeded,
		ExitCode:      0,
		DurationMS:    500,
		OutputBytes:   1024,
		ArtifactCount: 1,
	}
	evt, err := domain.NewDomainEvent("evt-int", domain.EventInvocationCompleted, "sess-x", "t1", "a1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the published data can be deserialized back
	var restored domain.DomainEvent
	if err := json.Unmarshal(fake.last().Data, &restored); err != nil {
		t.Fatalf("failed to unmarshal published data: %v", err)
	}
	if restored.ID != "evt-int" {
		t.Fatalf("expected event id evt-int, got %s", restored.ID)
	}
	if restored.Type != domain.EventInvocationCompleted {
		t.Fatalf("expected type invocation.completed, got %s", restored.Type)
	}

	var decoded domain.InvocationCompletedPayload
	if err := json.Unmarshal(restored.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.ToolName != "fs.read" {
		t.Fatalf("expected tool_name fs.read, got %s", decoded.ToolName)
	}
	if decoded.DurationMS != 500 {
		t.Fatalf("expected duration_ms 500, got %d", decoded.DurationMS)
	}
}

func TestNATSPublisher_MultiplePublish(t *testing.T) {
	fake := &fakeJSPublisher{}
	pub := NewNATSPublisher(fake, "")
	ctx := context.Background()

	events := []domain.EventType{
		domain.EventSessionCreated,
		domain.EventInvocationStarted,
		domain.EventInvocationCompleted,
		domain.EventSessionClosed,
	}

	for i, et := range events {
		evt := makeNATSTestEvent(t, "evt-multi-"+string(rune('0'+i)), et)
		if err := pub.Publish(ctx, evt); err != nil {
			t.Fatalf("unexpected error for event %d: %v", i, err)
		}
	}

	if fake.count() != 4 {
		t.Fatalf("expected 4 messages, got %d", fake.count())
	}
}

func TestNewNATSPublisherFromURL_Unreachable(t *testing.T) {
	_, _, err := NewNATSPublisherFromURL(context.Background(), "nats://127.0.0.1:14222", "")
	if err == nil {
		t.Fatal("expected connection error for unreachable NATS")
	}
}

func TestNewNATSPublisherFromURL_EmptyStreamName(t *testing.T) {
	_, _, err := NewNATSPublisherFromURL(context.Background(), "nats://127.0.0.1:14222", "  ")
	if err == nil {
		t.Fatal("expected connection error for unreachable NATS")
	}
}

func TestEventTypeSuffix(t *testing.T) {
	tests := []struct {
		input    domain.EventType
		expected string
	}{
		{domain.EventSessionCreated, "session.created"},
		{domain.EventSessionClosed, "session.closed"},
		{domain.EventInvocationStarted, "invocation.started"},
		{domain.EventInvocationCompleted, "invocation.completed"},
		{domain.EventInvocationDenied, "invocation.denied"},
		{domain.EventArtifactStored, "artifact.stored"},
		{domain.EventType("simple"), "simple"},
	}

	for _, tt := range tests {
		got := eventTypeSuffix(tt.input)
		if got != tt.expected {
			t.Fatalf("eventTypeSuffix(%q): expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}
