package telemetry

import (
	"context"
	"log/slog"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// --- Noop Observer ---

// NoopQualityObserver discards all quality metrics. Used when observability
// is disabled or in tests.
type NoopQualityObserver struct{}

func (NoopQualityObserver) ObserveInvocationQuality(_ context.Context, _ domain.InvocationQualityMetrics, _ domain.QualityObservationContext) {
}

// --- Slog Observer (structured logs) ---

// SlogQualityObserver emits quality metrics as structured log records.
// When LOG_FORMAT=json, these become JSON lines consumable by Loki/Promtail.
type SlogQualityObserver struct {
	logger *slog.Logger
}

// NewSlogQualityObserver creates a structured-log observer.
func NewSlogQualityObserver(logger *slog.Logger) *SlogQualityObserver {
	return &SlogQualityObserver{logger: logger}
}

func (o *SlogQualityObserver) ObserveInvocationQuality(_ context.Context, m domain.InvocationQualityMetrics, obsCtx domain.QualityObservationContext) {
	o.logger.Info("invocation.quality",
		slog.String("tool", m.ToolName()),
		slog.String("status", string(m.Status())),
		slog.Int64("duration_ms", m.DurationMS()),
		slog.Int("exit_code", m.ExitCode()),
		slog.String("latency_bucket", m.LatencyBucket()),
		slog.Float64("success_rate", m.SuccessRate()),
		slog.Bool("has_error", m.HasError()),
		slog.String("error_code", m.ErrorCode()),
		slog.String("session_id", obsCtx.SessionID),
		slog.String("tenant_id", obsCtx.TenantID),
		slog.String("actor_id", obsCtx.ActorID),
	)
}

// --- Composite Observer (fan-out) ---

// CompositeQualityObserver fans out quality observations to multiple
// observers. Calls are synchronous; wrap in a goroutine at the call
// site if non-blocking behavior is needed.
type CompositeQualityObserver struct {
	observers []interface {
		ObserveInvocationQuality(context.Context, domain.InvocationQualityMetrics, domain.QualityObservationContext)
	}
}

// NewCompositeQualityObserver creates a fan-out observer.
func NewCompositeQualityObserver(observers ...interface {
	ObserveInvocationQuality(context.Context, domain.InvocationQualityMetrics, domain.QualityObservationContext)
}) *CompositeQualityObserver {
	return &CompositeQualityObserver{observers: observers}
}

func (c *CompositeQualityObserver) ObserveInvocationQuality(ctx context.Context, m domain.InvocationQualityMetrics, obsCtx domain.QualityObservationContext) {
	for _, obs := range c.observers {
		obs.ObserveInvocationQuality(ctx, m, obsCtx)
	}
}
