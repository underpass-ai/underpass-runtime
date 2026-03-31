package domain

import "strings"

// HyLinUCBPolicyComputer wraps HyLinUCB to satisfy the PolicyComputer
// interface used by ComputePolicyUseCase. It manages feature encoding
// internally from ContextSignature and AggregateStats.
type HyLinUCBPolicyComputer struct {
	scorer *HyLinUCB
}

// NewHyLinUCBPolicyComputer creates a PolicyComputer backed by HyLinUCB.
func NewHyLinUCBPolicyComputer(alpha float64) *HyLinUCBPolicyComputer {
	return &HyLinUCBPolicyComputer{
		scorer: NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, alpha),
	}
}

// ComputePolicy computes a ToolPolicy using HyLinUCB contextual scoring.
// It encodes features from the context signature, feeds the observation
// to the model, and converts the UCB score to a ToolPolicy.
func (h *HyLinUCBPolicyComputer) ComputePolicy(contextSig, toolID string, stats AggregateStats) ToolPolicy {
	sig := ParseContextSignature(contextSig)
	ctxFeatures := EncodeContextFeatures(sig)
	armFeatures := EncodeToolFeatures("low", "none", "free", false)
	z := EncodeSharedFeatures(ctxFeatures, armFeatures)

	// Feed observation to update the model.
	if stats.Total > 0 {
		reward := float64(stats.Successes) / float64(stats.Total)
		h.scorer.Update(toolID, ctxFeatures, z, reward)
	}

	// Score the tool in this context.
	score := h.scorer.Score(toolID, ctxFeatures, z)

	alpha := float64(stats.Successes) + 1
	beta := float64(stats.Failures) + 1

	return ToolPolicy{
		ContextSignature: contextSig,
		ToolID:           toolID,
		Alpha:            alpha,
		Beta:             beta,
		Confidence:       score,
		P95LatencyMs:     stats.P95LatencyMs,
		P95Cost:          stats.P95Cost,
		ErrorRate:        stats.ErrorRate,
		NSamples:         stats.Total,
	}
}

// ParseContextSignature parses a "family:lang:constraints" key back
// into a ContextSignature struct.
func ParseContextSignature(key string) ContextSignature {
	parts := strings.SplitN(key, ":", 3)
	sig := ContextSignature{}
	if len(parts) >= 1 {
		sig.TaskFamily = parts[0]
	}
	if len(parts) >= 2 {
		sig.Lang = parts[1]
	}
	if len(parts) >= 3 {
		sig.ConstraintsClass = parts[2]
	}
	return sig
}
