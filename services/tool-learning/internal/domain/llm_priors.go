package domain

import "math"

// ToolPrior represents an LLM-generated prior belief about a tool's
// effectiveness for a given context. The LLM estimates a success
// probability which is converted to Beta distribution parameters.
//
// Reference: Alamdari, Cao, Wilson. "Jump Starting Bandits with
// LLM-Generated Prior Knowledge." EMNLP 2024.
type ToolPrior struct {
	ToolID      string  `json:"tool_id"`
	EstimatedP  float64 `json:"estimated_p"`         // LLM's estimated success probability [0, 1]
	EquivalentN float64 `json:"equivalent_n"`        // equivalent sample size (confidence in the estimate)
	Alpha       float64 `json:"alpha"`               // Beta shape: estimated_p * equivalent_n
	Beta        float64 `json:"beta"`                // Beta shape: (1 - estimated_p) * equivalent_n
	Rationale   string  `json:"rationale,omitempty"` // LLM's reasoning (for audit)
}

// PriorConfig controls how LLM estimates are converted to Beta priors.
type PriorConfig struct {
	// EquivalentN is the default equivalent sample size for LLM estimates.
	// Higher = more trust in the LLM's prior. Lower = faster adaptation.
	// Recommended: 5-20. Default: 10.
	EquivalentN float64

	// MinP clamps the estimated probability to avoid degenerate priors.
	// Default: 0.01 (1%).
	MinP float64

	// MaxP clamps the estimated probability to avoid overconfident priors.
	// Default: 0.99 (99%).
	MaxP float64
}

// DefaultPriorConfig returns sensible defaults.
func DefaultPriorConfig() PriorConfig {
	return PriorConfig{
		EquivalentN: 10,
		MinP:        0.01,
		MaxP:        0.99,
	}
}

// ComputePrior converts an LLM-estimated success probability into
// Beta distribution parameters.
//
// The math: if the LLM estimates p=0.8 with equivalentN=10,
// the prior is Beta(8, 2) — equivalent to having seen 8 successes
// and 2 failures already. This gives the tool a head start instead
// of the uninformative Beta(1, 1).
func ComputePrior(toolID string, estimatedP float64, cfg PriorConfig) ToolPrior {
	if cfg.EquivalentN <= 0 {
		cfg.EquivalentN = 10
	}
	p := clamp(estimatedP, cfg.MinP, cfg.MaxP)
	alpha := p * cfg.EquivalentN
	beta := (1 - p) * cfg.EquivalentN

	return ToolPrior{
		ToolID:      toolID,
		EstimatedP:  p,
		EquivalentN: cfg.EquivalentN,
		Alpha:       alpha,
		Beta:        beta,
	}
}

// PriorMap is a map of toolID → ToolPrior for use by the sampler.
type PriorMap map[string]ToolPrior

// NewThompsonSamplerWithPriors creates a sampler that uses per-tool
// LLM-generated priors instead of uniform Beta(1,1).
func NewThompsonSamplerWithPriors(priors PriorMap) *ThompsonSamplerLLM {
	return &ThompsonSamplerLLM{
		priors:       priors,
		defaultAlpha: 1.0,
		defaultBeta:  1.0,
	}
}

// ThompsonSamplerLLM extends ThompsonSampler with per-tool priors.
type ThompsonSamplerLLM struct {
	priors       PriorMap
	defaultAlpha float64
	defaultBeta  float64
}

// ComputePolicy computes a ToolPolicy using the tool's specific prior
// if available, falling back to uniform Beta(1,1) otherwise.
func (s *ThompsonSamplerLLM) ComputePolicy(contextSig, toolID string, stats AggregateStats) ToolPolicy {
	priorAlpha := s.defaultAlpha
	priorBeta := s.defaultBeta

	if prior, ok := s.priors[toolID]; ok {
		priorAlpha = prior.Alpha
		priorBeta = prior.Beta
	}

	alpha := float64(stats.Successes) + priorAlpha
	beta := float64(stats.Failures) + priorBeta
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

// Sample draws from Beta(alpha, beta) for ranking — same as ThompsonSampler.
func (s *ThompsonSamplerLLM) Sample(p ToolPolicy) float64 {
	return betaSample(p.Alpha, p.Beta)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// LLMPriorPrompt generates the prompt to send to an LLM for generating
// tool priors. The LLM should respond with a JSON array of
// {"tool_id": "...", "estimated_p": 0.XX, "rationale": "..."}.
func LLMPriorPrompt(tools []ToolDescription, context ContextSignature) string {
	return `You are an expert software engineering assistant evaluating tool effectiveness.

Given the following context:
- Task family: ` + context.TaskFamily + `
- Language: ` + context.Lang + `
- Constraints: ` + context.ConstraintsClass + `

For each tool below, estimate the probability (0.0 to 1.0) that it will
succeed when invoked by an agent in this context. Consider:
- How relevant the tool is for this task/language combination
- The tool's risk level and side effects
- Whether the tool typically succeeds in automated contexts

Respond with ONLY a JSON array:
[{"tool_id": "fs.write_file", "estimated_p": 0.95, "rationale": "file write is fundamental and low-risk"}, ...]

Tools:
` + formatToolDescriptions(tools)
}

// ToolDescription is a minimal tool description for the LLM prompt.
type ToolDescription struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
	SideEffects string `json:"side_effects"`
	Cost        string `json:"cost"`
}

func formatToolDescriptions(tools []ToolDescription) string {
	result := ""
	for _, t := range tools {
		result += "- " + t.ID + ": " + t.Description +
			" (risk=" + t.Risk + ", effects=" + t.SideEffects + ", cost=" + t.Cost + ")\n"
	}
	return result
}
