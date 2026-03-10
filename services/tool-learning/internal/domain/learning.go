package domain

import (
	"math"
	"math/rand/v2"
)

// PolicyConstraints defines hard SLO filters applied before sampling.
// Tools violating any constraint are excluded from ranking.
type PolicyConstraints struct {
	MaxP95LatencyMs int64   `json:"max_p95_latency_ms"`
	MaxErrorRate    float64 `json:"max_error_rate"`
	MaxP95Cost      float64 `json:"max_p95_cost"`
}

// IsEligible returns true if the policy passes all hard constraints.
func (c PolicyConstraints) IsEligible(p ToolPolicy) bool {
	if c.MaxP95LatencyMs > 0 && p.P95LatencyMs > c.MaxP95LatencyMs {
		return false
	}
	if c.MaxErrorRate > 0 && p.ErrorRate > c.MaxErrorRate {
		return false
	}
	if c.MaxP95Cost > 0 && p.P95Cost > c.MaxP95Cost {
		return false
	}
	return true
}

// ThompsonSampler implements Thompson Sampling with Beta priors
// for tool selection ranking.
type ThompsonSampler struct {
	PriorAlpha float64
	PriorBeta  float64
}

// NewThompsonSampler creates a sampler with default uniform priors (1, 1).
func NewThompsonSampler() *ThompsonSampler {
	return &ThompsonSampler{PriorAlpha: 1.0, PriorBeta: 1.0}
}

// ComputePolicy computes a ToolPolicy from an AggregateStats result.
func (s *ThompsonSampler) ComputePolicy(contextSig, toolID string, stats AggregateStats) ToolPolicy {
	alpha := float64(stats.Successes) + s.PriorAlpha
	beta := float64(stats.Failures) + s.PriorBeta
	confidence := alpha / (alpha + beta)

	return ToolPolicy{
		ContextSignature: contextSig,
		ToolID:           toolID,
		Alpha:            alpha,
		Beta:             beta,
		P95LatencyMs:     stats.P95LatencyMs,
		P95Cost:          stats.P95Cost,
		ErrorRate:        stats.ErrorRate,
		NSamples:         stats.Total,
		Confidence:       confidence,
	}
}

// Sample draws a random score from Beta(alpha, beta) for ranking.
// Higher scores indicate higher expected success probability.
func (s *ThompsonSampler) Sample(p ToolPolicy) float64 {
	return betaSample(p.Alpha, p.Beta)
}

// betaSample draws from a Beta distribution using the gamma trick:
// X ~ Gamma(alpha, 1), Y ~ Gamma(beta, 1) => X/(X+Y) ~ Beta(alpha, beta).
func betaSample(alpha, beta float64) float64 {
	x := gammaSample(alpha)
	y := gammaSample(beta)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// gammaSample draws from Gamma(shape, 1) using Marsaglia and Tsang's method.
func gammaSample(shape float64) float64 {
	if shape < 1 {
		u := rand.Float64()
		return gammaSample(shape+1) * math.Pow(u, 1.0/shape)
	}
	d := shape - 1.0/3.0
	c := 1.0 / (3.0 * math.Sqrt(d))
	for {
		var x float64
		for {
			x = rand.NormFloat64()
			if 1+c*x > 0 {
				break
			}
		}
		v := (1 + c*x) * (1 + c*x) * (1 + c*x)
		u := rand.Float64()
		if u < 1-0.0331*(x*x)*(x*x) {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

// AggregateStats holds the raw aggregate computed by DuckDB.
type AggregateStats struct {
	ContextSignature string
	ToolID           string
	Total            int64
	Successes        int64
	Failures         int64
	P95LatencyMs     int64
	P95Cost          float64
	ErrorRate        float64
}
