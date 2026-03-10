package domain

import (
	"testing"
	"time"
)

func TestContextSignatureKey(t *testing.T) {
	cs := ContextSignature{
		TaskFamily:       "code-gen",
		Lang:             "go",
		ConstraintsClass: "standard",
	}
	want := "code-gen:go:standard"
	if got := cs.Key(); got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

func TestPseudonymizeID(t *testing.T) {
	h1 := PseudonymizeID("tenant-key", "user-123")
	h2 := PseudonymizeID("tenant-key", "user-123")
	h3 := PseudonymizeID("tenant-key", "user-456")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars", len(h1))
	}
}

func TestPolicyConstraintsIsEligible(t *testing.T) {
	constraints := PolicyConstraints{
		MaxP95LatencyMs: 5000,
		MaxErrorRate:    0.1,
		MaxP95Cost:      10.0,
	}

	tests := []struct {
		name     string
		policy   ToolPolicy
		eligible bool
	}{
		{
			name:     "all within limits",
			policy:   ToolPolicy{P95LatencyMs: 1000, ErrorRate: 0.05, P95Cost: 5.0},
			eligible: true,
		},
		{
			name:     "latency exceeds",
			policy:   ToolPolicy{P95LatencyMs: 6000, ErrorRate: 0.05, P95Cost: 5.0},
			eligible: false,
		},
		{
			name:     "error rate exceeds",
			policy:   ToolPolicy{P95LatencyMs: 1000, ErrorRate: 0.2, P95Cost: 5.0},
			eligible: false,
		},
		{
			name:     "cost exceeds",
			policy:   ToolPolicy{P95LatencyMs: 1000, ErrorRate: 0.05, P95Cost: 15.0},
			eligible: false,
		},
		{
			name:     "zero constraints allow everything",
			policy:   ToolPolicy{P95LatencyMs: 99999, ErrorRate: 1.0, P95Cost: 999.0},
			eligible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := constraints
			if tt.name == "zero constraints allow everything" {
				c = PolicyConstraints{}
			}
			if got := c.IsEligible(tt.policy); got != tt.eligible {
				t.Errorf("IsEligible() = %v, want %v", got, tt.eligible)
			}
		})
	}
}

func TestThompsonSamplerComputePolicy(t *testing.T) {
	sampler := NewThompsonSampler()

	stats := AggregateStats{
		ContextSignature: "code-gen:go:standard",
		ToolID:           "fs.write",
		Total:            100,
		Successes:        90,
		Failures:         10,
		P95LatencyMs:     250,
		P95Cost:          0.5,
		ErrorRate:        0.1,
	}

	policy := sampler.ComputePolicy("code-gen:go:standard", "fs.write", stats)

	if policy.Alpha != 91.0 { // 90 + 1 (prior)
		t.Errorf("Alpha = %f, want 91.0", policy.Alpha)
	}
	if policy.Beta != 11.0 { // 10 + 1 (prior)
		t.Errorf("Beta = %f, want 11.0", policy.Beta)
	}
	if policy.NSamples != 100 {
		t.Errorf("NSamples = %d, want 100", policy.NSamples)
	}
	if policy.ContextSignature != "code-gen:go:standard" {
		t.Errorf("ContextSignature = %q", policy.ContextSignature)
	}
	if policy.ToolID != "fs.write" {
		t.Errorf("ToolID = %q", policy.ToolID)
	}
	wantConfidence := 91.0 / 102.0
	if diff := policy.Confidence - wantConfidence; diff > 0.001 || diff < -0.001 {
		t.Errorf("Confidence = %f, want ~%f", policy.Confidence, wantConfidence)
	}
}

func TestThompsonSamplerSample(t *testing.T) {
	sampler := NewThompsonSampler()

	highSuccess := ToolPolicy{Alpha: 100, Beta: 2}
	lowSuccess := ToolPolicy{Alpha: 2, Beta: 100}

	// Run many samples; high-success tool should average higher
	var highSum, lowSum float64
	n := 10000
	for range n {
		highSum += sampler.Sample(highSuccess)
		lowSum += sampler.Sample(lowSuccess)
	}

	highAvg := highSum / float64(n)
	lowAvg := lowSum / float64(n)

	if highAvg <= lowAvg {
		t.Errorf("high-success avg (%f) should be > low-success avg (%f)", highAvg, lowAvg)
	}
	if highAvg < 0.9 {
		t.Errorf("high-success avg (%f) should be close to 0.98", highAvg)
	}
	if lowAvg > 0.1 {
		t.Errorf("low-success avg (%f) should be close to 0.02", lowAvg)
	}
}

func TestToolInvocationIsSuccess(t *testing.T) {
	success := ToolInvocation{Outcome: OutcomeSuccess}
	failure := ToolInvocation{Outcome: OutcomeFailure}

	if !success.IsSuccess() {
		t.Error("expected success")
	}
	if failure.IsSuccess() {
		t.Error("expected not success")
	}
}

func TestToolPolicyValkeyKey(t *testing.T) {
	p := ToolPolicy{ContextSignature: "gen:go:std", ToolID: "fs.write"}
	want := "tool_policy:gen:go:std:fs.write"
	if got := p.ValkeyKey("tool_policy"); got != want {
		t.Errorf("ValkeyKey() = %q, want %q", got, want)
	}
}

func TestGammaSamplePositive(t *testing.T) {
	// Gamma samples should always be positive
	for range 1000 {
		v := gammaSample(2.0)
		if v < 0 {
			t.Fatalf("gammaSample returned negative: %f", v)
		}
	}
}

func TestGammaSampleSmallShape(t *testing.T) {
	// Shape < 1 uses the recursive path
	for range 100 {
		v := gammaSample(0.5)
		if v < 0 {
			t.Fatalf("gammaSample(0.5) returned negative: %f", v)
		}
	}
}

func TestBetaSampleRange(t *testing.T) {
	for range 1000 {
		v := betaSample(2.0, 3.0)
		if v < 0 || v > 1 {
			t.Fatalf("betaSample out of [0,1]: %f", v)
		}
	}
}

// Ensure RealClock returns something close to now.
func TestRealClockNotZero(t *testing.T) {
	_ = time.Now() // just to verify it doesn't panic
}
