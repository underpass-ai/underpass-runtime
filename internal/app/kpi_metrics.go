package app

import (
	"fmt"
	"strings"
	"sync"
)

// KPIMetrics tracks learning-loop KPI metrics for Prometheus exposition.
// All methods are safe for concurrent use.
type KPIMetrics struct {
	mu sync.RWMutex

	// workspace_tool_calls_per_task: tool invocations grouped by task context
	toolCallsPerTask map[string]uint64

	// workspace_success_on_first_tool: first invocation per session succeeded
	firstToolSuccess uint64
	firstToolTotal   uint64

	// workspace_recommendation_acceptance_rate: recommended tool was actually used
	recommendedUsed  uint64
	recommendedTotal uint64

	// workspace_policy_denial_rate_bad_recommendation: denials after recommendation
	policyDenialAfterRec  uint64
	policyDenialAfterRecN uint64

	// workspace_context_bytes_saved: bytes saved by compact discovery
	contextBytesSaved int64
}

// NewKPIMetrics creates a new KPI metrics tracker.
func NewKPIMetrics() *KPIMetrics {
	return &KPIMetrics{
		toolCallsPerTask: map[string]uint64{},
	}
}

// ObserveToolCall records a tool invocation for the given task context.
func (k *KPIMetrics) ObserveToolCall(taskContext string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if taskContext == "" {
		taskContext = "unknown"
	}
	k.toolCallsPerTask[taskContext]++
}

// ObserveFirstToolResult records whether the first tool invocation in a session
// succeeded or not.
func (k *KPIMetrics) ObserveFirstToolResult(succeeded bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.firstToolTotal++
	if succeeded {
		k.firstToolSuccess++
	}
}

// ObserveRecommendationUsed records when a recommended tool was actually invoked.
func (k *KPIMetrics) ObserveRecommendationUsed(used bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.recommendedTotal++
	if used {
		k.recommendedUsed++
	}
}

// ObservePolicyDenialAfterRecommendation records a policy denial on a recommended tool.
func (k *KPIMetrics) ObservePolicyDenialAfterRecommendation(denied bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.policyDenialAfterRecN++
	if denied {
		k.policyDenialAfterRec++
	}
}

// ObserveContextBytesSaved records bytes saved by using compact discovery.
func (k *KPIMetrics) ObserveContextBytesSaved(bytes int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.contextBytesSaved += bytes
}

// PrometheusText returns Prometheus exposition format text for all KPI metrics.
func (k *KPIMetrics) PrometheusText() string {
	k.mu.RLock()
	defer k.mu.RUnlock()

	var b strings.Builder

	b.WriteString("# HELP workspace_tool_calls_per_task Total tool invocations by task context.\n")
	b.WriteString("# TYPE workspace_tool_calls_per_task counter\n")
	for _, task := range sortedInnerKeys(k.toolCallsPerTask) {
		b.WriteString(fmt.Sprintf( //nolint:gocritic // Prometheus exposition format requires explicit quotes
			"workspace_tool_calls_per_task{task=\"%s\"} %d\n",
			escapePrometheusLabelValue(task),
			k.toolCallsPerTask[task],
		))
	}

	b.WriteString("# HELP workspace_success_on_first_tool Whether the first tool in a session succeeded.\n")
	b.WriteString("# TYPE workspace_success_on_first_tool gauge\n")
	rate := float64(0)
	if k.firstToolTotal > 0 {
		rate = float64(k.firstToolSuccess) / float64(k.firstToolTotal)
	}
	b.WriteString(fmt.Sprintf("workspace_success_on_first_tool_rate %f\n", rate))
	b.WriteString(fmt.Sprintf("workspace_success_on_first_tool_total %d\n", k.firstToolTotal))

	b.WriteString("# HELP workspace_recommendation_acceptance_rate Rate of recommended tools actually used.\n")
	b.WriteString("# TYPE workspace_recommendation_acceptance_rate gauge\n")
	recRate := float64(0)
	if k.recommendedTotal > 0 {
		recRate = float64(k.recommendedUsed) / float64(k.recommendedTotal)
	}
	b.WriteString(fmt.Sprintf("workspace_recommendation_acceptance_rate %f\n", recRate))
	b.WriteString(fmt.Sprintf("workspace_recommendation_total %d\n", k.recommendedTotal))

	b.WriteString("# HELP workspace_policy_denial_rate_bad_recommendation Rate of policy denials on recommended tools.\n")
	b.WriteString("# TYPE workspace_policy_denial_rate_bad_recommendation gauge\n")
	denialRate := float64(0)
	if k.policyDenialAfterRecN > 0 {
		denialRate = float64(k.policyDenialAfterRec) / float64(k.policyDenialAfterRecN)
	}
	b.WriteString(fmt.Sprintf("workspace_policy_denial_rate_bad_recommendation %f\n", denialRate))

	b.WriteString("# HELP workspace_context_bytes_saved Total bytes saved by compact discovery.\n")
	b.WriteString("# TYPE workspace_context_bytes_saved counter\n")
	b.WriteString(fmt.Sprintf("workspace_context_bytes_saved %d\n", k.contextBytesSaved))

	return b.String()
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
