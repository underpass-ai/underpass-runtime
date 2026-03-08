package eventbus

import (
	"context"
	"log/slog"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// NoopPublisher implements app.EventPublisher by logging events at debug level.
type NoopPublisher struct {
	logger *slog.Logger
}

// NewNoopPublisher creates a NoopPublisher. If logger is nil, slog.Default() is used.
func NewNoopPublisher(logger *slog.Logger) *NoopPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &NoopPublisher{logger: logger}
}

func (p *NoopPublisher) Publish(_ context.Context, event domain.DomainEvent) error {
	p.logger.Debug("domain event (noop)",
		"event_id", event.ID,
		"event_type", string(event.Type),
		"session_id", event.SessionID,
		"tenant_id", event.TenantID,
	)
	return nil
}
