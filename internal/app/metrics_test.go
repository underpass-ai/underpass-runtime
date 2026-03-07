package app

import (
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestInvocationMetricsPrometheusText(t *testing.T) {
	metrics := newInvocationMetrics()
	metrics.Observe(domain.Invocation{
		ToolName:   "fs.read_file",
		Status:     domain.InvocationStatusSucceeded,
		DurationMS: 12,
	})
	metrics.Observe(domain.Invocation{
		ToolName:   "fs.read_file",
		Status:     domain.InvocationStatusDenied,
		DurationMS: 3,
		Error: &domain.Error{
			Message: "session invocation rate limit exceeded",
		},
	})
	metrics.Observe(domain.Invocation{
		ToolName:   "git.push",
		Status:     domain.InvocationStatusFailed,
		DurationMS: 1300,
		Error: &domain.Error{
			Code:    ErrorCodeInternal,
			Message: "boom",
		},
	})

	payload := metrics.PrometheusText()

	requiredSnippets := []string{
		"invocations_total{tool=\"fs.read_file\",status=\"succeeded\"} 1",
		"invocations_total{tool=\"fs.read_file\",status=\"denied\"} 1",
		"invocations_total{tool=\"git.push\",status=\"failed\"} 1",
		"denied_total{tool=\"fs.read_file\",reason=\"session invocation rate limit exceeded\"} 1",
		"duration_ms_count{tool=\"fs.read_file\"} 2",
		"duration_ms_count{tool=\"git.push\"} 1",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(payload, snippet) {
			t.Fatalf("expected metrics output to contain %q, got:\n%s", snippet, payload)
		}
	}
}
