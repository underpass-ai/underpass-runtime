package app

import (
	"strings"
	"testing"
)

func TestKPIMetrics_ToolCallsPerTask(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveToolCall("build")
	kpi.ObserveToolCall("build")
	kpi.ObserveToolCall("test")
	kpi.ObserveToolCall("") // defaults to "unknown"

	text := kpi.PrometheusText()
	if !strings.Contains(text, `workspace_tool_calls_per_task{task="build"} 2`) {
		t.Fatalf("expected build=2 in output:\n%s", text)
	}
	if !strings.Contains(text, `workspace_tool_calls_per_task{task="test"} 1`) {
		t.Fatalf("expected test=1 in output:\n%s", text)
	}
	if !strings.Contains(text, `workspace_tool_calls_per_task{task="unknown"} 1`) {
		t.Fatalf("expected unknown=1 in output:\n%s", text)
	}
}

func TestKPIMetrics_SuccessOnFirstTool(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveFirstToolResult(true)
	kpi.ObserveFirstToolResult(true)
	kpi.ObserveFirstToolResult(false)

	text := kpi.PrometheusText()
	// 2/3 ≈ 0.666667
	if !strings.Contains(text, "workspace_success_on_first_tool_total 3") {
		t.Fatalf("expected total=3 in output:\n%s", text)
	}
	if !strings.Contains(text, "workspace_success_on_first_tool_rate 0.6") {
		t.Fatalf("expected rate ~0.667 in output:\n%s", text)
	}
}

func TestKPIMetrics_SuccessOnFirstTool_Empty(t *testing.T) {
	kpi := NewKPIMetrics()
	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_success_on_first_tool_rate 0.0") {
		t.Fatalf("expected rate 0 when empty:\n%s", text)
	}
}

func TestKPIMetrics_RecommendationAcceptance(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveRecommendationUsed(true)
	kpi.ObserveRecommendationUsed(true)
	kpi.ObserveRecommendationUsed(false)

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_recommendation_total 3") {
		t.Fatalf("expected total=3:\n%s", text)
	}
	if !strings.Contains(text, "workspace_recommendation_acceptance_rate 0.6") {
		t.Fatalf("expected rate ~0.667:\n%s", text)
	}
}

func TestKPIMetrics_PolicyDenialAfterRecommendation(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObservePolicyDenialAfterRecommendation(true)
	kpi.ObservePolicyDenialAfterRecommendation(false)

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_policy_denial_rate_bad_recommendation 0.5") {
		t.Fatalf("expected 50%% denial rate:\n%s", text)
	}
}

func TestKPIMetrics_PolicyDenialAfterRecommendation_Empty(t *testing.T) {
	kpi := NewKPIMetrics()
	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_policy_denial_rate_bad_recommendation 0.0") {
		t.Fatalf("expected 0 rate when empty:\n%s", text)
	}
}

func TestKPIMetrics_ContextBytesSaved(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveContextBytesSaved(1024)
	kpi.ObserveContextBytesSaved(2048)

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_context_bytes_saved 3072") {
		t.Fatalf("expected 3072 bytes saved:\n%s", text)
	}
}

func TestKPIMetrics_SessionsCreated(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveSessionCreated()
	kpi.ObserveSessionCreated()
	kpi.ObserveSessionCreated()

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_sessions_created_total 3") {
		t.Fatalf("expected sessions_created=3 in output:\n%s", text)
	}
}

func TestKPIMetrics_SessionsClosed(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveSessionClosed()
	kpi.ObserveSessionClosed()

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_sessions_closed_total 2") {
		t.Fatalf("expected sessions_closed=2 in output:\n%s", text)
	}
}

func TestKPIMetrics_DiscoveryRequests(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveDiscoveryRequest()
	kpi.ObserveDiscoveryRequest()
	kpi.ObserveDiscoveryRequest()
	kpi.ObserveDiscoveryRequest()

	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_discovery_requests_total 4") {
		t.Fatalf("expected discovery_requests=4 in output:\n%s", text)
	}
}

func TestKPIMetrics_InvocationsDenied(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveInvocationDenied("policy_denied")
	kpi.ObserveInvocationDenied("policy_denied")
	kpi.ObserveInvocationDenied("approval_required")
	kpi.ObserveInvocationDenied("") // defaults to "unspecified"

	text := kpi.PrometheusText()
	if !strings.Contains(text, `workspace_invocations_denied_total{reason="policy_denied"} 2`) {
		t.Fatalf("expected policy_denied=2 in output:\n%s", text)
	}
	if !strings.Contains(text, `workspace_invocations_denied_total{reason="approval_required"} 1`) {
		t.Fatalf("expected approval_required=1 in output:\n%s", text)
	}
	if !strings.Contains(text, `workspace_invocations_denied_total{reason="unspecified"} 1`) {
		t.Fatalf("expected unspecified=1 in output:\n%s", text)
	}
}

func TestKPIMetrics_InvocationsDenied_Empty(t *testing.T) {
	kpi := NewKPIMetrics()
	text := kpi.PrometheusText()
	// No denied_total lines should appear when there are no denials.
	if strings.Contains(text, "workspace_invocations_denied_total{") {
		t.Fatalf("expected no denied lines when empty:\n%s", text)
	}
	// But the HELP/TYPE header should still be present.
	if !strings.Contains(text, "# HELP workspace_invocations_denied_total") {
		t.Fatalf("expected HELP header for denied_total:\n%s", text)
	}
}

func TestKPIMetrics_SessionCounters_ZeroDefault(t *testing.T) {
	kpi := NewKPIMetrics()
	text := kpi.PrometheusText()
	if !strings.Contains(text, "workspace_sessions_created_total 0") {
		t.Fatalf("expected sessions_created=0 by default:\n%s", text)
	}
	if !strings.Contains(text, "workspace_sessions_closed_total 0") {
		t.Fatalf("expected sessions_closed=0 by default:\n%s", text)
	}
	if !strings.Contains(text, "workspace_discovery_requests_total 0") {
		t.Fatalf("expected discovery_requests=0 by default:\n%s", text)
	}
}

func TestKPIMetrics_PrometheusText_AllSections(t *testing.T) {
	kpi := NewKPIMetrics()
	kpi.ObserveToolCall("build")
	kpi.ObserveFirstToolResult(true)
	kpi.ObserveRecommendationUsed(true)
	kpi.ObservePolicyDenialAfterRecommendation(false)
	kpi.ObserveContextBytesSaved(512)
	kpi.ObserveSessionCreated()
	kpi.ObserveSessionClosed()
	kpi.ObserveDiscoveryRequest()
	kpi.ObserveInvocationDenied("policy_denied")

	text := kpi.PrometheusText()

	expectedSections := []string{
		"workspace_tool_calls_per_task",
		"workspace_success_on_first_tool_rate",
		"workspace_recommendation_acceptance_rate",
		"workspace_policy_denial_rate_bad_recommendation",
		"workspace_context_bytes_saved",
		"workspace_sessions_created_total",
		"workspace_sessions_closed_total",
		"workspace_discovery_requests_total",
		"workspace_invocations_denied_total",
	}
	for _, section := range expectedSections {
		if !strings.Contains(text, section) {
			t.Errorf("missing section %s in prometheus text:\n%s", section, text)
		}
	}
}

func TestKPIMetrics_ConcurrentAccess(t *testing.T) {
	kpi := NewKPIMetrics()
	done := make(chan struct{})

	for range 10 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 100 {
				kpi.ObserveToolCall("test")
				kpi.ObserveFirstToolResult(true)
				kpi.ObserveRecommendationUsed(true)
				kpi.ObservePolicyDenialAfterRecommendation(false)
				kpi.ObserveContextBytesSaved(10)
				kpi.ObserveSessionCreated()
				kpi.ObserveSessionClosed()
				kpi.ObserveDiscoveryRequest()
				kpi.ObserveInvocationDenied("policy_denied")
				_ = kpi.PrometheusText()
			}
		}()
	}

	for range 10 {
		<-done
	}

	// Just verify it didn't panic or race
	text := kpi.PrometheusText()
	if text == "" {
		t.Fatal("expected non-empty prometheus text")
	}
}
