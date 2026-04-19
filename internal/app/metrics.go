package app

import (
	"context"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricapi "go.opentelemetry.io/otel/metric"
)

var invocationDurationHistogramBuckets = []int64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

type invocationMetrics struct {
	invocations metricapi.Int64Counter
	denied      metricapi.Int64Counter
	durations   metricapi.Int64Histogram
}

func newInvocationMetrics() *invocationMetrics {
	return newInvocationMetricsWithMeter(appMeter())
}

func newInvocationMetricsWithMeter(meter metricapi.Meter) *invocationMetrics {
	invocations, err := meter.Int64Counter(
		"invocations_total",
		metricapi.WithDescription("Total tool invocations by tool and final status."),
	)
	if err != nil {
		otel.Handle(err)
	}
	denied, err := meter.Int64Counter(
		"denied_total",
		metricapi.WithDescription("Total denied invocations by tool and reason."),
	)
	if err != nil {
		otel.Handle(err)
	}
	durations, err := meter.Int64Histogram(
		"duration_ms",
		metricapi.WithDescription("Tool invocation duration histogram in milliseconds by tool."),
		metricapi.WithUnit("ms"),
	)
	if err != nil {
		otel.Handle(err)
	}
	return &invocationMetrics{
		invocations: invocations,
		denied:      denied,
		durations:   durations,
	}
}

func (m *invocationMetrics) Observe(invocation domain.Invocation) {
	tool := normalizeMetricValue(invocation.ToolName, "unknown")
	status := normalizeMetricValue(string(invocation.Status), "unknown")
	reason := normalizeDeniedReason(invocation.Error)
	duration := invocation.DurationMS
	if duration < 0 {
		duration = 0
	}

	m.invocations.Add(
		context.Background(),
		1,
		metricapi.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("status", status),
		),
	)

	if invocation.Status == domain.InvocationStatusDenied {
		m.denied.Add(
			context.Background(),
			1,
			metricapi.WithAttributes(
				attribute.String("tool", tool),
				attribute.String("reason", reason),
			),
		)
	}

	m.durations.Record(
		context.Background(),
		duration,
		metricapi.WithAttributes(attribute.String("tool", tool)),
	)
}

func (m *invocationMetrics) PrometheusText() string {
	return renderPrometheusText(func(name string) bool {
		switch name {
		case "invocations_total", "denied_total", "duration_ms":
			return true
		default:
			return false
		}
	})
}

func normalizeMetricValue(raw, fallback string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func normalizeDeniedReason(err *domain.Error) string {
	if err == nil {
		return "unspecified"
	}
	reason := strings.TrimSpace(err.Message)
	if reason == "" {
		reason = strings.TrimSpace(err.Code)
	}
	if reason == "" {
		reason = "unspecified"
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 160 {
		reason = reason[:160]
	}
	return reason
}

func escapePrometheusLabelValue(raw string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(raw)
}
