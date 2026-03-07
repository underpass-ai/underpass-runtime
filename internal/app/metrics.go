package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

var invocationDurationHistogramBuckets = []int64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

type invocationMetrics struct {
	mu sync.RWMutex

	invocations map[string]map[string]uint64
	denied      map[string]map[string]uint64
	durations   map[string]*durationHistogram
}

type durationHistogram struct {
	bounds      []int64
	bucketCount []uint64
	sum         float64
	count       uint64
}

func newInvocationMetrics() *invocationMetrics {
	return &invocationMetrics{
		invocations: map[string]map[string]uint64{},
		denied:      map[string]map[string]uint64{},
		durations:   map[string]*durationHistogram{},
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, found := m.invocations[tool]; !found {
		m.invocations[tool] = map[string]uint64{}
	}
	m.invocations[tool][status]++

	if invocation.Status == domain.InvocationStatusDenied {
		if _, found := m.denied[tool]; !found {
			m.denied[tool] = map[string]uint64{}
		}
		m.denied[tool][reason]++
	}

	histogram, found := m.durations[tool]
	if !found {
		histogram = newDurationHistogram(invocationDurationHistogramBuckets)
		m.durations[tool] = histogram
	}
	histogram.Observe(duration)
}

func (m *invocationMetrics) PrometheusText() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var builder strings.Builder
	builder.WriteString("# HELP invocations_total Total tool invocations by tool and final status.\n")
	builder.WriteString("# TYPE invocations_total counter\n")
	for _, tool := range sortedNestedKeys(m.invocations) {
		for _, status := range sortedInnerKeys(m.invocations[tool]) {
			builder.WriteString(fmt.Sprintf(
				"invocations_total{tool=\"%s\",status=\"%s\"} %d\n",
				escapePrometheusLabelValue(tool),
				escapePrometheusLabelValue(status),
				m.invocations[tool][status],
			))
		}
	}

	builder.WriteString("# HELP denied_total Total denied invocations by tool and reason.\n")
	builder.WriteString("# TYPE denied_total counter\n")
	for _, tool := range sortedNestedKeys(m.denied) {
		for _, reason := range sortedInnerKeys(m.denied[tool]) {
			builder.WriteString(fmt.Sprintf(
				"denied_total{tool=\"%s\",reason=\"%s\"} %d\n",
				escapePrometheusLabelValue(tool),
				escapePrometheusLabelValue(reason),
				m.denied[tool][reason],
			))
		}
	}

	builder.WriteString("# HELP duration_ms Tool invocation duration histogram in milliseconds by tool.\n")
	builder.WriteString("# TYPE duration_ms histogram\n")
	for _, tool := range sortedHistogramTools(m.durations) {
		histogram := m.durations[tool]
		cumulative := uint64(0)
		for idx, upperBound := range histogram.bounds {
			cumulative += histogram.bucketCount[idx]
			builder.WriteString(fmt.Sprintf(
				"duration_ms_bucket{tool=\"%s\",le=\"%s\"} %d\n",
				escapePrometheusLabelValue(tool),
				strconv.FormatInt(upperBound, 10),
				cumulative,
			))
		}
		builder.WriteString(fmt.Sprintf(
			"duration_ms_bucket{tool=\"%s\",le=\"+Inf\"} %d\n",
			escapePrometheusLabelValue(tool),
			histogram.count,
		))
		builder.WriteString(fmt.Sprintf(
			"duration_ms_sum{tool=\"%s\"} %.3f\n",
			escapePrometheusLabelValue(tool),
			histogram.sum,
		))
		builder.WriteString(fmt.Sprintf(
			"duration_ms_count{tool=\"%s\"} %d\n",
			escapePrometheusLabelValue(tool),
			histogram.count,
		))
	}

	return builder.String()
}

func newDurationHistogram(bounds []int64) *durationHistogram {
	normalized := make([]int64, 0, len(bounds))
	for _, bound := range bounds {
		if bound > 0 {
			normalized = append(normalized, bound)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i] < normalized[j]
	})
	return &durationHistogram{
		bounds:      normalized,
		bucketCount: make([]uint64, len(normalized)),
	}
}

func (h *durationHistogram) Observe(durationMS int64) {
	h.count++
	h.sum += float64(durationMS)
	if len(h.bounds) == 0 {
		return
	}
	for idx, upperBound := range h.bounds {
		if durationMS <= upperBound {
			h.bucketCount[idx]++
			return
		}
	}
}

func sortedNestedKeys(input map[string]map[string]uint64) []string {
	out := make([]string, 0, len(input))
	for key := range input {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedInnerKeys(input map[string]uint64) []string {
	out := make([]string, 0, len(input))
	for key := range input {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedHistogramTools(input map[string]*durationHistogram) []string {
	out := make([]string, 0, len(input))
	for key := range input {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
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
