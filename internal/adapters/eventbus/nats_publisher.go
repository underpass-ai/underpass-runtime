package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const defaultStreamName = "WORKSPACE_EVENTS"

// jsPublisher is the minimal JetStream interface used by NATSPublisher.
type jsPublisher interface {
	Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// NATSPublisher implements app.EventPublisher by publishing domain events to
// NATS JetStream. Subject pattern: workspace.events.{event_type_suffix}.
// Message deduplication uses the event ID as Nats-Msg-Id.
type NATSPublisher struct {
	js            jsPublisher
	subjectPrefix string
}

// NewNATSPublisher creates a publisher from a pre-configured JetStream context.
func NewNATSPublisher(js jsPublisher, subjectPrefix string) *NATSPublisher {
	prefix := strings.TrimSpace(subjectPrefix)
	if prefix == "" {
		prefix = "workspace.events"
	}
	return &NATSPublisher{js: js, subjectPrefix: prefix}
}

// NewNATSPublisherFromURL connects to NATS, obtains a JetStream context, and
// ensures the stream exists. The stream is created if absent.
func NewNATSPublisherFromURL(ctx context.Context, natsURL, streamName string) (*NATSPublisher, *nats.Conn, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("jetstream context: %w", err)
	}

	stream := strings.TrimSpace(streamName)
	if stream == "" {
		stream = defaultStreamName
	}

	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     stream,
		Subjects: []string{"workspace.events.>"},
	}); err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("jetstream stream create: %w", err)
	}

	pub := NewNATSPublisher(js, "workspace.events")
	return pub, nc, nil
}

// Publish serializes the domain event as JSON and publishes it to JetStream
// with deduplication via the event ID.
func (p *NATSPublisher) Publish(ctx context.Context, event domain.DomainEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("nats marshal event: %w", err)
	}

	subject := p.subjectPrefix + "." + eventTypeSuffix(event.Type)

	_, err = p.js.Publish(ctx, subject, data, jetstream.WithMsgID(event.ID))
	if err != nil {
		return fmt.Errorf("nats publish %s: %w", subject, err)
	}
	return nil
}

// eventTypeSuffix extracts the last segment after the last dot.
// e.g. "workspace.session.created" → "session.created"
func eventTypeSuffix(t domain.EventType) string {
	s := string(t)
	if idx := strings.Index(s, "."); idx >= 0 {
		return s[idx+1:]
	}
	return s
}
