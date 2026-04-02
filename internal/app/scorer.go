package app

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// RecommendationScorer computes a score adjustment for a tool based on its
// learned policy. Implementations correspond to different algorithm families.
type RecommendationScorer interface {
	// Score returns the adjusted score and explanation for a tool given its
	// base heuristic score and learned policy.
	Score(base float64, policy ToolPolicy) (score float64, why string)
	// AlgorithmID returns the canonical identifier for this scorer.
	AlgorithmID() string
	// AlgorithmVersion returns the version string.
	AlgorithmVersion() string
}

// ─── Thompson Sampling Scorer ──────────────────────────────────────────────

// ThompsonScorer uses Beta-distribution sampling from the learned policy
// posteriors (alpha, beta). The score is a Thompson sample — a random draw
// from Beta(alpha, beta) — which naturally balances exploration and
// exploitation.
type ThompsonScorer struct{}

func (ThompsonScorer) AlgorithmID() string      { return "beta_thompson_sampling" }
func (ThompsonScorer) AlgorithmVersion() string { return "1.0.0" }

func (ThompsonScorer) Score(base float64, policy ToolPolicy) (float64, string) {
	if policy.NSamples < learnedMinSamples {
		return base, ""
	}

	// Draw from Beta(alpha, beta) via the gamma trick.
	sample := betaSample(policy.Alpha, policy.Beta)

	// Blend: weighted combination of heuristic base and Thompson sample.
	// As n_samples grows, trust the learned sample more.
	weight := math.Min(float64(policy.NSamples)/100.0, 0.8)
	blended := (1-weight)*base + weight*sample

	// Apply SLO penalties on top.
	if policy.ErrorRate > learnedErrorRateThreshold {
		blended -= learnedErrorRatePenalty
	}
	if policy.P95LatencyMs > learnedLatencyThreshold {
		blended -= learnedLatencyPenalty
	}

	blended = math.Round(blended*100) / 100
	why := fmt.Sprintf("thompson(sample=%.2f, weight=%.2f, conf=%.2f, n=%d)",
		sample, weight, policy.Confidence, policy.NSamples)
	return blended, why
}

// betaSample draws from Beta(alpha, beta) using the gamma distribution trick.
func betaSample(alpha, beta float64) float64 {
	if alpha <= 0 {
		alpha = 1
	}
	if beta <= 0 {
		beta = 1
	}
	x := gammaVariate(alpha)
	y := gammaVariate(beta)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// gammaVariate draws from Gamma(shape, 1) using Marsaglia-Tsang method.
func gammaVariate(shape float64) float64 {
	if shape < 1 {
		return gammaVariate(shape+1) * math.Pow(rand.Float64(), 1.0/shape)
	}
	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)
	for {
		var x, v float64
		for {
			x = rand.NormFloat64()
			v = 1.0 + c*x
			if v > 0 {
				break
			}
		}
		v = v * v * v
		u := rand.Float64()
		if u < 1.0-0.0331*(x*x)*(x*x) {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}
}

// ─── Heuristic Policy Scorer ───────────────────────────────────────────────

// HeuristicPolicyScorer is the original fixed-weight scorer (P0-P2 behavior).
// Used as fallback when insufficient data for Thompson sampling.
type HeuristicPolicyScorer struct{}

func (HeuristicPolicyScorer) AlgorithmID() string      { return AlgorithmIDHeuristic }
func (HeuristicPolicyScorer) AlgorithmVersion() string { return AlgorithmVersionV1 }

func (HeuristicPolicyScorer) Score(base float64, policy ToolPolicy) (float64, string) {
	if policy.NSamples < learnedMinSamples {
		return base, ""
	}

	score := base
	score += learnedConfidenceBoost * policy.Confidence

	var why string
	if policy.ErrorRate > learnedErrorRateThreshold {
		score -= learnedErrorRatePenalty
		why += fmt.Sprintf(", learned: high error rate %.0f%%", policy.ErrorRate*100)
	}
	if policy.P95LatencyMs > learnedLatencyThreshold {
		score -= learnedLatencyPenalty
		why += fmt.Sprintf(", learned: slow p95 %dms", policy.P95LatencyMs)
	}

	why += fmt.Sprintf(", learned policy (confidence=%.2f, n=%d)", policy.Confidence, policy.NSamples)
	score = math.Round(score*100) / 100
	return score, why
}

// ─── Algorithm Selection ───────────────────────────────────────────────────

// SelectScorer picks the best scorer based on available policy data.
func SelectScorer(policies map[string]ToolPolicy) RecommendationScorer {
	return SelectScorerWithModel(policies, nil)
}

// SelectScorerWithModel picks the best scorer considering a trained neural model.
// Priority: NeuralTS > Thompson > Heuristic.
func SelectScorerWithModel(policies map[string]ToolPolicy, model *MLPWeights) RecommendationScorer {
	if len(policies) == 0 {
		return nil
	}

	var maxSamples int64
	for _, p := range policies {
		if p.NSamples > maxSamples {
			maxSamples = p.NSamples
		}
	}

	// NeuralTS when trained model available and sufficient data.
	if model != nil && model.Valid() && maxSamples >= 100 {
		return NeuralTSScorer{Model: model}
	}

	// Thompson sampling for moderate data.
	if maxSamples >= 50 {
		return ThompsonScorer{}
	}

	// Heuristic fallback.
	return HeuristicPolicyScorer{}
}
