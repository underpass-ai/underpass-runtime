package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	defaultSubjectPrefix = "workspace.events"
	headerMsgID          = "Nats-Msg-Id"
)

// jsPublisher abstracts JetStream publishing for testability.
type jsPublisher interface {
	Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// NATSPublisher implements app.EventPublisher using NATS JetStream.
// Events are published with message deduplication via the event ID header.
type NATSPublisher struct {
	js            jsPublisher
	subjectPrefix string
	logger        *slog.Logger
}

// NATSPublisherConfig holds configuration for NATSPublisher.
type NATSPublisherConfig struct {
	URL           string // NATS server URL
	SubjectPrefix string // subject prefix (default: "workspace.events")
	Logger        *slog.Logger
}

// NewNATSPublisher creates a NATSPublisher connected to the given NATS server.
// The stream must already exist — this publisher does not auto-create streams.
func NewNATSPublisher(cfg NATSPublisherConfig) (*NATSPublisher, error) {
	nc, err := nats.Connect(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream init: %w", err)
	}

	prefix := cfg.SubjectPrefix
	if prefix == "" {
		prefix = defaultSubjectPrefix
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &NATSPublisher{
		js:            js,
		subjectPrefix: prefix,
		logger:        logger,
	}, nil
}

// newNATSPublisherWithJS creates a NATSPublisher with an injected jsPublisher (for testing).
func newNATSPublisherWithJS(js jsPublisher, subjectPrefix string, logger *slog.Logger) *NATSPublisher {
	if subjectPrefix == "" {
		subjectPrefix = defaultSubjectPrefix
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &NATSPublisher{
		js:            js,
		subjectPrefix: subjectPrefix,
		logger:        logger,
	}
}

func (p *NATSPublisher) Publish(ctx context.Context, event domain.DomainEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	subject := p.subjectPrefix + "." + string(event.Type)

	_, err = p.js.Publish(ctx, subject, data, jetstream.WithMsgID(event.ID))
	if err != nil {
		p.logger.Warn("event publish failed",
			"event_id", event.ID,
			"event_type", string(event.Type),
			"subject", subject,
			"error", err.Error(),
		)
		return fmt.Errorf("jetstream publish: %w", err)
	}

	p.logger.Debug("event published",
		"event_id", event.ID,
		"event_type", string(event.Type),
		"subject", subject,
	)
	return nil
}
