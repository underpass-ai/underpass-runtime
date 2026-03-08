package app

import (
	"testing"
)

func TestApplyTelemetryBoost_NotEnoughData(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 3, SuccessRate: 1.0}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score != 1.0 {
		t.Fatalf("should not boost with < %d invocations: got %f", telSuccessMinN, boosted.Score)
	}
}

func TestApplyTelemetryBoost_HighSuccess(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.95}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score <= 1.0 {
		t.Fatalf("expected score increase for high success rate, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_LowSuccess(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.30}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score >= 1.0 {
		t.Fatalf("expected score decrease for low success rate, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_SlowP95(t *testing.T) {
	rec := Recommendation{Name: "repo.build", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.70, P95Duration: 15000}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score >= 1.0 {
		t.Fatalf("expected penalty for slow p95, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_HighDenyRate(t *testing.T) {
	rec := Recommendation{Name: "k8s.apply", Score: 1.0, Why: "high risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.70, DenyRate: 0.30}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score >= 1.0 {
		t.Fatalf("expected penalty for high deny rate, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_CombinedEffects(t *testing.T) {
	rec := Recommendation{Name: "slow.tool", Score: 1.0, Why: "start"}
	// Bad tool: low success, slow, high deny
	stats := ToolStats{
		InvocationN: 20,
		SuccessRate: 0.30,
		P95Duration: 20000,
		DenyRate:    0.40,
	}

	boosted := applyTelemetryBoost(rec, stats)
	expectedMax := 1.0 - telSuccessBonus - telDurationPenP95 - telDenyPenalty
	if boosted.Score > expectedMax {
		t.Fatalf("expected score <= %f, got %f", expectedMax, boosted.Score)
	}
}

func TestApplyTelemetryBoost_ReasonAnnotation(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.95}

	boosted := applyTelemetryBoost(rec, stats)
	if len(boosted.Why) <= len(rec.Why) {
		t.Fatalf("expected reason to be augmented, got: %s", boosted.Why)
	}
}

func TestApplyTelemetryBoost_MidRangeSuccess(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	// 70% success: neither high (>90%) nor low (<50%) — no adjustment
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.70}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score != 1.0 {
		t.Fatalf("mid-range success should not change score, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_FastP95NoSlowPenalty(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.70, P95Duration: 500}

	boosted := applyTelemetryBoost(rec, stats)
	// No slow penalty for 500ms p95
	if boosted.Score < 1.0 {
		t.Fatalf("fast p95 should not be penalized, got %f", boosted.Score)
	}
}

func TestApplyTelemetryBoost_LowDenyNopenalty(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	stats := ToolStats{InvocationN: 10, SuccessRate: 0.70, DenyRate: 0.05}

	boosted := applyTelemetryBoost(rec, stats)
	if boosted.Score < 1.0 {
		t.Fatalf("low deny rate should not be penalized, got %f", boosted.Score)
	}
}
