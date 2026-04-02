package app

import (
	"encoding/json"
	"testing"
)

func TestMLPWeights_Valid(t *testing.T) {
	m := NewRandomMLPWeights()
	if !m.Valid() {
		t.Fatal("random weights should be valid")
	}
	if m.ParamCount() != neuralInputDim*neuralHiddenDim+neuralHiddenDim+neuralHiddenDim*neuralOutputDim+neuralOutputDim {
		t.Fatalf("unexpected param count: %d", m.ParamCount())
	}
}

func TestMLPWeights_Forward_Deterministic(t *testing.T) {
	m := NewRandomMLPWeights()
	x := make([]float64, neuralInputDim)
	x[0] = 1.0

	a := m.Forward(x)
	b := m.Forward(x)
	if a != b {
		t.Fatalf("deterministic forward should return same value: %f vs %f", a, b)
	}
}

func TestMLPWeights_ForwardPerturbed_Varies(t *testing.T) {
	m := NewRandomMLPWeights()
	x := make([]float64, neuralInputDim)
	x[0] = 1.0

	seen := make(map[float64]bool)
	for range 10 {
		v := m.ForwardPerturbed(x, 0.5)
		seen[v] = true
	}
	if len(seen) < 2 {
		t.Fatal("perturbed forward should produce varied outputs")
	}
}

func TestMLPWeights_SerializeRoundTrip(t *testing.T) {
	m := NewRandomMLPWeights()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	m2, err := UnmarshalMLPWeights(data)
	if err != nil {
		t.Fatal(err)
	}

	if m2.ParamCount() != m.ParamCount() {
		t.Fatal("param count mismatch after roundtrip")
	}

	// Check values match.
	for i, v := range m.W1 {
		if v != m2.W1[i] {
			t.Fatalf("W1[%d] mismatch: %f vs %f", i, v, m2.W1[i])
		}
	}
}

func TestUnmarshalMLPWeights_InvalidDimensions(t *testing.T) {
	_, err := UnmarshalMLPWeights([]byte(`{"w1":[],"b1":[],"w2":[],"b2":[]}`))
	if err == nil {
		t.Fatal("expected error for invalid dimensions")
	}
}

func TestNeuralTSScorer_NoModel(t *testing.T) {
	s := NeuralTSScorer{Model: nil}
	score, why := s.Score(1.0, ToolPolicy{NSamples: 100})
	if score != 1.0 {
		t.Fatalf("expected base score with nil model, got %f", score)
	}
	if why != "" {
		t.Fatalf("expected empty why, got %q", why)
	}
}

func TestNeuralTSScorer_WithModel(t *testing.T) {
	m := NewRandomMLPWeights()
	s := NeuralTSScorer{Model: m}

	policy := ToolPolicy{
		Alpha: 90, Beta: 10, Confidence: 0.9,
		NSamples: 200, ErrorRate: 0.05, P95LatencyMs: 150,
	}
	score, why := s.Score(1.0, policy)
	if why == "" {
		t.Fatal("expected non-empty why")
	}
	if score < -1.0 || score > 3.0 {
		t.Fatalf("score %f out of reasonable range", score)
	}
	if s.AlgorithmID() != "neural_thompson_sampling" {
		t.Fatalf("unexpected algorithm ID: %s", s.AlgorithmID())
	}
}

func TestNeuralTSScorer_InsufficientSamples(t *testing.T) {
	m := NewRandomMLPWeights()
	s := NeuralTSScorer{Model: m}
	score, why := s.Score(1.0, ToolPolicy{NSamples: 5})
	if score != 1.0 || why != "" {
		t.Fatal("should return base for low samples")
	}
}

func TestSelectScorerWithModel_PrefersNeural(t *testing.T) {
	m := NewRandomMLPWeights()
	policies := map[string]ToolPolicy{
		"fs.read": {NSamples: 200, Confidence: 0.9},
	}
	s := SelectScorerWithModel(policies, m)
	if s == nil {
		t.Fatal("expected non-nil scorer")
	}
	if s.AlgorithmID() != "neural_thompson_sampling" {
		t.Fatalf("expected neural_ts, got %s", s.AlgorithmID())
	}
}

func TestSelectScorerWithModel_FallsBackWithoutModel(t *testing.T) {
	policies := map[string]ToolPolicy{
		"fs.read": {NSamples: 200, Confidence: 0.9},
	}
	s := SelectScorerWithModel(policies, nil)
	if s.AlgorithmID() != "beta_thompson_sampling" {
		t.Fatalf("expected thompson fallback, got %s", s.AlgorithmID())
	}
}

func TestPolicyToFeatures_Length(t *testing.T) {
	f := policyToFeatures(ToolPolicy{
		Confidence: 0.9, ErrorRate: 0.05, P95LatencyMs: 200,
		NSamples: 100, Alpha: 90, Beta: 10, P95Cost: 0.01,
	})
	if len(f) != neuralInputDim {
		t.Fatalf("expected %d features, got %d", neuralInputDim, len(f))
	}
}
