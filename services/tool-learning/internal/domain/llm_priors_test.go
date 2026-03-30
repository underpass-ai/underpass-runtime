package domain

import (
	"math"
	"testing"
)

func TestComputePrior_BasicConversion(t *testing.T) {
	cfg := DefaultPriorConfig()
	prior := ComputePrior("fs.write_file", 0.8, cfg)

	if prior.ToolID != "fs.write_file" {
		t.Errorf("ToolID = %q, want fs.write_file", prior.ToolID)
	}
	// p=0.8, n=10 → Alpha=8, Beta=2
	if math.Abs(prior.Alpha-8.0) > 0.001 {
		t.Errorf("Alpha = %f, want 8.0", prior.Alpha)
	}
	if math.Abs(prior.Beta-2.0) > 0.001 {
		t.Errorf("Beta = %f, want 2.0", prior.Beta)
	}
}

func TestComputePrior_ClampLow(t *testing.T) {
	cfg := DefaultPriorConfig()
	prior := ComputePrior("bad.tool", 0.0, cfg) // clamped to MinP=0.01

	if prior.EstimatedP != 0.01 {
		t.Errorf("EstimatedP = %f, want 0.01 (clamped)", prior.EstimatedP)
	}
	if prior.Alpha < 0.01 {
		t.Errorf("Alpha should be positive, got %f", prior.Alpha)
	}
}

func TestComputePrior_ClampHigh(t *testing.T) {
	cfg := DefaultPriorConfig()
	prior := ComputePrior("perfect.tool", 1.0, cfg) // clamped to MaxP=0.99

	if prior.EstimatedP != 0.99 {
		t.Errorf("EstimatedP = %f, want 0.99 (clamped)", prior.EstimatedP)
	}
}

func TestComputePrior_CustomEquivalentN(t *testing.T) {
	cfg := PriorConfig{EquivalentN: 20, MinP: 0.01, MaxP: 0.99}
	prior := ComputePrior("git.push", 0.7, cfg)

	// p=0.7, n=20 → Alpha=14, Beta=6
	if math.Abs(prior.Alpha-14.0) > 0.001 {
		t.Errorf("Alpha = %f, want 14.0", prior.Alpha)
	}
	if math.Abs(prior.Beta-6.0) > 0.001 {
		t.Errorf("Beta = %f, want 6.0", prior.Beta)
	}
}

func TestThompsonSamplerLLM_UsesPerToolPriors(t *testing.T) {
	priors := PriorMap{
		"fs.write_file": ComputePrior("fs.write_file", 0.95, DefaultPriorConfig()),
		"k8s.apply":     ComputePrior("k8s.apply", 0.30, DefaultPriorConfig()),
	}
	sampler := NewThompsonSamplerWithPriors(priors)

	// Both tools have zero observations.
	statsEmpty := AggregateStats{
		Total: 0, Successes: 0, Failures: 0,
	}

	policyGood := sampler.ComputePolicy("gen:go:std", "fs.write_file", statsEmpty)
	policyBad := sampler.ComputePolicy("gen:go:std", "k8s.apply", statsEmpty)

	// fs.write_file prior: Beta(9.5, 0.5) → confidence ≈ 0.95
	// k8s.apply prior: Beta(3.0, 7.0) → confidence ≈ 0.30
	if policyGood.Confidence <= policyBad.Confidence {
		t.Errorf("fs.write_file confidence (%f) should be > k8s.apply (%f)",
			policyGood.Confidence, policyBad.Confidence)
	}
}

func TestThompsonSamplerLLM_FallbackToUniform(t *testing.T) {
	priors := PriorMap{} // no priors
	sampler := NewThompsonSamplerWithPriors(priors)

	stats := AggregateStats{Total: 10, Successes: 8, Failures: 2}
	policy := sampler.ComputePolicy("gen:go:std", "unknown.tool", stats)

	// Should fall back to Beta(1,1) prior → Alpha=9, Beta=3
	if math.Abs(policy.Alpha-9.0) > 0.001 {
		t.Errorf("Alpha = %f, want 9.0 (uniform prior fallback)", policy.Alpha)
	}
}

func TestThompsonSamplerLLM_PriorsAccumulateWithData(t *testing.T) {
	priors := PriorMap{
		"fs.read_file": ComputePrior("fs.read_file", 0.9, DefaultPriorConfig()),
	}
	sampler := NewThompsonSamplerWithPriors(priors)

	// Prior: Beta(9, 1). Add 10 successes, 0 failures.
	stats := AggregateStats{Total: 10, Successes: 10, Failures: 0}
	policy := sampler.ComputePolicy("gen:go:std", "fs.read_file", stats)

	// Alpha = 10 + 9 = 19, Beta = 0 + 1 = 1
	if math.Abs(policy.Alpha-19.0) > 0.001 {
		t.Errorf("Alpha = %f, want 19.0", policy.Alpha)
	}
	if math.Abs(policy.Beta-1.0) > 0.001 {
		t.Errorf("Beta = %f, want 1.0", policy.Beta)
	}
}

func TestThompsonSamplerLLM_SampleRange(t *testing.T) {
	priors := PriorMap{
		"fs.list": ComputePrior("fs.list", 0.8, DefaultPriorConfig()),
	}
	sampler := NewThompsonSamplerWithPriors(priors)

	policy := ToolPolicy{Alpha: 8, Beta: 2}
	for range 1000 {
		v := sampler.Sample(policy)
		if v < 0 || v > 1 {
			t.Fatalf("sample out of [0,1]: %f", v)
		}
	}
}

func TestLLMPriorPrompt_ContainsContext(t *testing.T) {
	tools := []ToolDescription{
		{ID: "fs.write_file", Description: "Write a file", Risk: "low", SideEffects: "reversible", Cost: "free"},
	}
	ctx := ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"}
	prompt := LLMPriorPrompt(tools, ctx)

	for _, want := range []string{"gen", "go", "std", "fs.write_file", "Write a file", "estimated_p"} {
		if !contains(prompt, want) {
			t.Errorf("prompt should contain %q", want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
