package app

import (
	"context"
	"strings"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricapi "go.opentelemetry.io/otel/metric"
)

// KPIMetrics tracks learning-loop KPI metrics using OpenTelemetry
// instruments. All methods are safe for concurrent use.
type KPIMetrics struct {
	toolCallsPerTask                  metricapi.Int64Counter
	firstToolTotal                    metricapi.Int64Counter
	firstToolRate                     metricapi.Float64Gauge
	recommendationTotal               metricapi.Int64Counter
	recommendationAcceptanceRate      metricapi.Float64Gauge
	policyDenialRateBadRecommendation metricapi.Float64Gauge
	contextBytesSaved                 metricapi.Int64Counter
	sessionsCreated                   metricapi.Int64Counter
	sessionsClosed                    metricapi.Int64Counter
	discoveryRequests                 metricapi.Int64Counter
	invocationsDenied                 metricapi.Int64Counter
	firstToolSuccessCount             atomic.Uint64
	firstToolTotalCount               atomic.Uint64
	recommendedUsedCount              atomic.Uint64
	recommendedTotalCount             atomic.Uint64
	policyDenialAfterRecommendation   atomic.Uint64
	policyDenialAfterRecommendationN  atomic.Uint64
}

// NewKPIMetrics creates a new KPI metrics tracker.
func NewKPIMetrics() *KPIMetrics {
	return NewKPIMetricsWithMeter(appMeter())
}

// NewKPIMetricsWithMeter creates a KPI metrics tracker using the provided meter.
func NewKPIMetricsWithMeter(meter metricapi.Meter) *KPIMetrics {
	toolCallsPerTask, err := meter.Int64Counter(
		"workspace_tool_calls_per_task",
		metricapi.WithDescription("Total tool invocations by task context."),
	)
	if err != nil {
		otel.Handle(err)
	}
	firstToolTotal, err := meter.Int64Counter(
		"workspace_success_on_first_tool_total",
		metricapi.WithDescription("Total first-tool outcomes recorded for a session."),
	)
	if err != nil {
		otel.Handle(err)
	}
	firstToolRate, err := meter.Float64Gauge(
		"workspace_success_on_first_tool_rate",
		metricapi.WithDescription("Whether the first tool in a session succeeded."),
	)
	if err != nil {
		otel.Handle(err)
	}
	recommendationTotal, err := meter.Int64Counter(
		"workspace_recommendation_total",
		metricapi.WithDescription("Total recommendation decisions recorded."),
	)
	if err != nil {
		otel.Handle(err)
	}
	recommendationAcceptanceRate, err := meter.Float64Gauge(
		"workspace_recommendation_acceptance_rate",
		metricapi.WithDescription("Rate of recommended tools actually used."),
	)
	if err != nil {
		otel.Handle(err)
	}
	policyDenialRateBadRecommendation, err := meter.Float64Gauge(
		"workspace_policy_denial_rate_bad_recommendation",
		metricapi.WithDescription("Rate of policy denials on recommended tools."),
	)
	if err != nil {
		otel.Handle(err)
	}
	contextBytesSaved, err := meter.Int64Counter(
		"workspace_context_bytes_saved",
		metricapi.WithDescription("Total bytes saved by compact discovery."),
	)
	if err != nil {
		otel.Handle(err)
	}
	sessionsCreated, err := meter.Int64Counter(
		"workspace_sessions_created_total",
		metricapi.WithDescription("Total sessions successfully created."),
	)
	if err != nil {
		otel.Handle(err)
	}
	sessionsClosed, err := meter.Int64Counter(
		"workspace_sessions_closed_total",
		metricapi.WithDescription("Total sessions successfully closed."),
	)
	if err != nil {
		otel.Handle(err)
	}
	discoveryRequests, err := meter.Int64Counter(
		"workspace_discovery_requests_total",
		metricapi.WithDescription("Total tool discovery requests served."),
	)
	if err != nil {
		otel.Handle(err)
	}
	invocationsDenied, err := meter.Int64Counter(
		"workspace_invocations_denied_total",
		metricapi.WithDescription("Total denied invocations by denial reason."),
	)
	if err != nil {
		otel.Handle(err)
	}

	k := &KPIMetrics{
		toolCallsPerTask:                  toolCallsPerTask,
		firstToolTotal:                    firstToolTotal,
		firstToolRate:                     firstToolRate,
		recommendationTotal:               recommendationTotal,
		recommendationAcceptanceRate:      recommendationAcceptanceRate,
		policyDenialRateBadRecommendation: policyDenialRateBadRecommendation,
		contextBytesSaved:                 contextBytesSaved,
		sessionsCreated:                   sessionsCreated,
		sessionsClosed:                    sessionsClosed,
		discoveryRequests:                 discoveryRequests,
		invocationsDenied:                 invocationsDenied,
	}

	k.firstToolTotal.Add(context.Background(), 0)
	k.recommendationTotal.Add(context.Background(), 0)
	k.contextBytesSaved.Add(context.Background(), 0)
	k.sessionsCreated.Add(context.Background(), 0)
	k.sessionsClosed.Add(context.Background(), 0)
	k.discoveryRequests.Add(context.Background(), 0)
	k.firstToolRate.Record(context.Background(), 0)
	k.recommendationAcceptanceRate.Record(context.Background(), 0)
	k.policyDenialRateBadRecommendation.Record(context.Background(), 0)

	return k
}

// ObserveToolCall records a tool invocation for the given task context.
func (k *KPIMetrics) ObserveToolCall(taskContext string) {
	task := strings.TrimSpace(taskContext)
	if task == "" {
		task = "unknown"
	}
	k.toolCallsPerTask.Add(
		context.Background(),
		1,
		metricapi.WithAttributes(attribute.String("task", task)),
	)
}

// ObserveFirstToolResult records whether the first tool invocation in a session
// succeeded or not.
func (k *KPIMetrics) ObserveFirstToolResult(succeeded bool) {
	total := k.firstToolTotalCount.Add(1)
	k.firstToolTotal.Add(context.Background(), 1)
	if succeeded {
		k.firstToolSuccessCount.Add(1)
	}
	k.firstToolRate.Record(context.Background(), ratio(k.firstToolSuccessCount.Load(), total))
}

// ObserveRecommendationUsed records when a recommended tool was actually invoked.
func (k *KPIMetrics) ObserveRecommendationUsed(used bool) {
	total := k.recommendedTotalCount.Add(1)
	k.recommendationTotal.Add(context.Background(), 1)
	if used {
		k.recommendedUsedCount.Add(1)
	}
	k.recommendationAcceptanceRate.Record(context.Background(), ratio(k.recommendedUsedCount.Load(), total))
}

// ObservePolicyDenialAfterRecommendation records a policy denial on a recommended tool.
func (k *KPIMetrics) ObservePolicyDenialAfterRecommendation(denied bool) {
	total := k.policyDenialAfterRecommendationN.Add(1)
	if denied {
		k.policyDenialAfterRecommendation.Add(1)
	}
	k.policyDenialRateBadRecommendation.Record(
		context.Background(),
		ratio(k.policyDenialAfterRecommendation.Load(), total),
	)
}

// ObserveContextBytesSaved records bytes saved by using compact discovery.
func (k *KPIMetrics) ObserveContextBytesSaved(bytes int64) {
	if bytes <= 0 {
		return
	}
	k.contextBytesSaved.Add(context.Background(), bytes)
}

// ObserveSessionCreated increments the sessions-created counter.
func (k *KPIMetrics) ObserveSessionCreated() {
	k.sessionsCreated.Add(context.Background(), 1)
}

// ObserveSessionClosed increments the sessions-closed counter.
func (k *KPIMetrics) ObserveSessionClosed() {
	k.sessionsClosed.Add(context.Background(), 1)
}

// ObserveDiscoveryRequest increments the discovery-requests counter.
func (k *KPIMetrics) ObserveDiscoveryRequest() {
	k.discoveryRequests.Add(context.Background(), 1)
}

// ObserveInvocationDenied increments the denied-invocations counter for the given reason.
func (k *KPIMetrics) ObserveInvocationDenied(reason string) {
	normalized := strings.TrimSpace(reason)
	if normalized == "" {
		normalized = "unspecified"
	}
	k.invocationsDenied.Add(
		context.Background(),
		1,
		metricapi.WithAttributes(attribute.String("reason", normalized)),
	)
}

// PrometheusText returns Prometheus exposition format text for all KPI metrics.
func (k *KPIMetrics) PrometheusText() string {
	return renderPrometheusText(func(name string) bool {
		switch name {
		case "workspace_tool_calls_per_task",
			"workspace_success_on_first_tool_rate",
			"workspace_success_on_first_tool_total",
			"workspace_recommendation_acceptance_rate",
			"workspace_recommendation_total",
			"workspace_policy_denial_rate_bad_recommendation",
			"workspace_context_bytes_saved",
			"workspace_sessions_created_total",
			"workspace_sessions_closed_total",
			"workspace_discovery_requests_total",
			"workspace_invocations_denied_total":
			return true
		default:
			return false
		}
	})
}

func ratio(numerator, denominator uint64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// KPIPrometheusMetrics returns the KPI metrics as Prometheus text. Called from
// the metrics endpoint handler.
func (s *Service) KPIPrometheusMetrics() string {
	if s.kpiMetrics == nil {
		return ""
	}
	return s.kpiMetrics.PrometheusText()
}

// SetKPIMetrics attaches a KPI metrics tracker to the service.
func (s *Service) SetKPIMetrics(kpi *KPIMetrics) {
	if kpi != nil {
		s.kpiMetrics = kpi
	}
}

// GetKPIMetrics returns the current KPI metrics tracker, creating one if needed.
func (s *Service) GetKPIMetrics() *KPIMetrics {
	return s.kpiMetrics
}

// TelemetryQuerier exposes read access to the service's telemetry querier.
func (s *Service) TelemetryQuerier() TelemetryQuerier {
	return s.telemetryQ
}
