package tlsutil

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// TraceLogHandler wraps a slog.Handler and injects trace_id and span_id
// from the OpenTelemetry span context into every log record.
type TraceLogHandler struct {
	inner slog.Handler
}

// NewTraceLogHandler wraps an existing handler with trace context injection.
func NewTraceLogHandler(inner slog.Handler) *TraceLogHandler {
	return &TraceLogHandler{inner: inner}
}

func (h *TraceLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *TraceLogHandler) Handle(ctx context.Context, record slog.Record) error {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.HasTraceID() {
		record.AddAttrs(slog.String("trace_id", spanCtx.TraceID().String()))
	}
	if spanCtx.HasSpanID() {
		record.AddAttrs(slog.String("span_id", spanCtx.SpanID().String()))
	}
	return h.inner.Handle(ctx, record)
}

func (h *TraceLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceLogHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *TraceLogHandler) WithGroup(name string) slog.Handler {
	return &TraceLogHandler{inner: h.inner.WithGroup(name)}
}
