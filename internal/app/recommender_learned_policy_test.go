package app

import (
	"testing"
)

func TestApplyLearnedPolicy_NotEnoughSamples(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	policy := ToolPolicy{NSamples: 5, Confidence: 0.8}

	result := applyLearnedPolicy(rec, policy)
	if result.Score != 1.0 {
		t.Fatalf("should not adjust with < %d samples: got %f", learnedMinSamples, result.Score)
	}
}

func TestApplyLearnedPolicy_ConfidenceBoost(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	policy := ToolPolicy{NSamples: 20, Confidence: 0.9, ErrorRate: 0.1, P95LatencyMs: 5000}

	result := applyLearnedPolicy(rec, policy)
	expectedBoost := learnedConfidenceBoost * 0.9
	if result.Score < 1.0+expectedBoost-0.01 {
		t.Fatalf("expected score boost from confidence: got %f, want >= %f", result.Score, 1.0+expectedBoost)
	}
}

func TestApplyLearnedPolicy_HighErrorPenalty(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	policy := ToolPolicy{NSamples: 20, Confidence: 0.5, ErrorRate: 0.5, P95LatencyMs: 5000}

	result := applyLearnedPolicy(rec, policy)
	if result.Score >= 1.0 {
		t.Fatalf("expected penalty for high error rate: got %f", result.Score)
	}
}

func TestApplyLearnedPolicy_HighLatencyPenalty(t *testing.T) {
	rec := Recommendation{Name: "fs.read", Score: 1.0, Why: "low risk"}
	policy := ToolPolicy{NSamples: 20, Confidence: 0.5, ErrorRate: 0.1, P95LatencyMs: 20000}

	result := applyLearnedPolicy(rec, policy)
	boost := learnedConfidenceBoost * 0.5
	expectedMax := 1.0 + boost - learnedLatencyPenalty + 0.01
	if result.Score > expectedMax {
		t.Fatalf("expected latency penalty: got %f, want <= %f", result.Score, expectedMax)
	}
}
