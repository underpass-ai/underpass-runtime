package telemetry

import (
	"context"
	"log/slog"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricapi "go.opentelemetry.io/otel/metric"
)

// --- Noop Observer ---

// NoopQualityObserver discards all quality metrics. Used when observability
// is disabled or in tests.
type NoopQualityObserver struct{}

func (NoopQualityObserver) ObserveInvocationQuality(_ context.Context, _ domain.InvocationQualityMetrics, _ domain.QualityObservationContext) {
}

// --- OTel Observer ---

// OTelQualityObserver emits invocation quality metrics through the OTel SDK.
type OTelQualityObserver struct {
	invocations metricapi.Int64Counter
	durations   metricapi.Int64Histogram
}

// NewOTelQualityObserver creates an OTel-backed quality observer.
func NewOTelQualityObserver(meter metricapi.Meter) *OTelQualityObserver {
	invocations, err := meter.Int64Counter(
		"workspace_invocation_quality_total",
		metricapi.WithDescription("Total invocation quality observations."),
	)
	if err != nil {
		otel.Handle(err)
	}
	durations, err := meter.Int64Histogram(
		"workspace_invocation_quality_duration_ms",
		metricapi.WithDescription("Invocation quality duration histogram in milliseconds."),
		metricapi.WithUnit("ms"),
	)
	if err != nil {
		otel.Handle(err)
	}
	return &OTelQualityObserver{
		invocations: invocations,
		durations:   durations,
	}
}

func (o *OTelQualityObserver) ObserveInvocationQuality(ctx context.Context, m domain.InvocationQualityMetrics, _ domain.QualityObservationContext) {
	if o == nil {
		return
	}
	duration := m.DurationMS()
	if duration < 0 {
		duration = 0
	}
	attrs := []attribute.KeyValue{
		attribute.String("tool", normalizeObserverValue(m.ToolName(), "unknown")),
		attribute.String("status", normalizeObserverValue(string(m.Status()), "unknown")),
		attribute.String("latency_bucket", normalizeObserverValue(m.LatencyBucket(), "unknown")),
		attribute.Bool("has_error", m.HasError()),
	}
	if code := strings.TrimSpace(m.ErrorCode()); code != "" {
		attrs = append(attrs, attribute.String("error_code", code))
	}
	o.invocations.Add(ctx, 1, metricapi.WithAttributes(attrs...))
	o.durations.Record(ctx, duration, metricapi.WithAttributes(attrs...))
}

func normalizeObserverValue(raw, fallback string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return fallback
	}
	return trimmed
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
