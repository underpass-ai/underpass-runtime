package app

import (
	"testing"
)

func TestThompsonScorer_InsufficientSamples(t *testing.T) {
	s := ThompsonScorer{}
	score, why := s.Score(1.0, ToolPolicy{NSamples: 5, Alpha: 5, Beta: 1})
	if score != 1.0 {
		t.Fatalf("expected no change with low samples, got %f", score)
	}
	if why != "" {
		t.Fatalf("expected empty why, got %q", why)
	}
}

func TestThompsonScorer_ReturnsValidScore(t *testing.T) {
	s := ThompsonScorer{}
	// High-confidence policy: alpha=95, beta=5 → ~0.95 mean
	policy := ToolPolicy{Alpha: 95, Beta: 5, Confidence: 0.95, NSamples: 100, ErrorRate: 0.05, P95LatencyMs: 200}
	score, why := s.Score(1.0, policy)
	if why == "" {
		t.Fatal("expected non-empty why for Thompson scorer")
	}
	if score < 0 || score > 2.0 {
		t.Fatalf("score %f out of reasonable range", score)
	}
	if s.AlgorithmID() != "beta_thompson_sampling" {
		t.Fatalf("expected beta_thompson_sampling, got %s", s.AlgorithmID())
	}
}

func TestThompsonScorer_PenalizesHighErrorRate(t *testing.T) {
	s := ThompsonScorer{}
	policy := ToolPolicy{Alpha: 50, Beta: 50, Confidence: 0.5, NSamples: 100, ErrorRate: 0.5, P95LatencyMs: 200}
	// Run multiple times to account for randomness
	total := 0.0
	n := 100
	for i := 0; i < n; i++ {
		score, _ := s.Score(1.0, policy)
		total += score
	}
	avg := total / float64(n)
	// With 50% error rate, penalty applied, average should be < 1.0
	if avg > 1.0 {
		t.Fatalf("expected average score < 1.0 with high error rate, got %f", avg)
	}
}

func TestHeuristicPolicyScorer_SameAsPrevious(t *testing.T) {
	s := HeuristicPolicyScorer{}
	policy := ToolPolicy{NSamples: 20, Confidence: 0.8, ErrorRate: 0.1, P95LatencyMs: 5000}
	score, why := s.Score(1.0, policy)
	expected := 1.0 + learnedConfidenceBoost*0.8
	if score != expected {
		t.Fatalf("expected %f, got %f", expected, score)
	}
	if why == "" {
		t.Fatal("expected non-empty why")
	}
	if s.AlgorithmID() != AlgorithmIDHeuristic {
		t.Fatalf("expected %s, got %s", AlgorithmIDHeuristic, s.AlgorithmID())
	}
}

func TestSelectScorer_NoPolicies(t *testing.T) {
	s := SelectScorer(nil)
	if s != nil {
		t.Fatal("expected nil scorer for empty policies")
	}
}

func TestSelectScorer_LowSamples_ReturnsHeuristic(t *testing.T) {
	policies := map[string]ToolPolicy{
		"fs.read": {NSamples: 20, Confidence: 0.8},
	}
	s := SelectScorer(policies)
	if s == nil {
		t.Fatal("expected non-nil scorer")
	}
	if s.AlgorithmID() != AlgorithmIDHeuristic {
		t.Fatalf("expected heuristic for low samples, got %s", s.AlgorithmID())
	}
}

func TestSelectScorer_HighSamples_ReturnsThompson(t *testing.T) {
	policies := map[string]ToolPolicy{
		"fs.read": {NSamples: 100, Alpha: 90, Beta: 10, Confidence: 0.9},
	}
	s := SelectScorer(policies)
	if s == nil {
		t.Fatal("expected non-nil scorer")
	}
	if s.AlgorithmID() != "beta_thompson_sampling" {
		t.Fatalf("expected thompson for high samples, got %s", s.AlgorithmID())
	}
}

func TestBetaSample_ValidRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := betaSample(10, 10)
		if s < 0 || s > 1 {
			t.Fatalf("betaSample out of [0,1]: %f", s)
		}
	}
}

func TestBetaSample_DegenerateInputs(t *testing.T) {
	s := betaSample(0, 0) // should clamp to Beta(1,1)
	if s < 0 || s > 1 {
		t.Fatalf("betaSample with zero params out of range: %f", s)
	}
}
