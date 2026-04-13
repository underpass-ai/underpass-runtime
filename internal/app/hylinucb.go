package app

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	hylinucbAlgorithmID = "hylinucb_hybrid"
	hylinucbVersion     = "1.0.0"
	hylinucbArmDim      = 13
	hylinucbSharedDim   = 17
	hylinucbAlpha       = 0.25
)

// HyLinUCBScorer implements RecommendationScorer using Hybrid Linear UCB.
// It maintains mutable state (matrix accumulation) per context signature,
// so it is session-scoped: learning resets across sessions but explores
// efficiently within one.
//
// Priority in the selection chain: fills the gap between Thompson (batch,
// offline) and NeuralTS (model-based). HyLinUCB provides fast online
// exploration with contextual features when learned policies exist but
// haven't reached NeuralTS thresholds.
type HyLinUCBScorer struct {
	instance *hyLinUCBInstance
}

func (s HyLinUCBScorer) AlgorithmID() string      { return hylinucbAlgorithmID }
func (s HyLinUCBScorer) AlgorithmVersion() string { return hylinucbVersion }

func (s HyLinUCBScorer) Score(base float64, policy ToolPolicy) (float64, string) {
	if policy.NSamples < learnedMinSamples {
		return base, ""
	}

	// Encode policy → arm-specific context features (x, 13-dim).
	x := policyToArmFeatures(policy)
	// Encode policy → shared features (z, 17-dim = context + arm metadata).
	z := policyToSharedFeatures(policy)

	// UCB score with exploration bonus.
	ucbScore := s.instance.score(policy.ToolID, x, z)

	// Blend with heuristic base — weight grows with samples.
	weight := math.Min(float64(policy.NSamples)/80.0, 0.7)
	blended := (1-weight)*base + weight*ucbScore

	// SLO penalties.
	if policy.ErrorRate > learnedErrorRateThreshold {
		blended -= learnedErrorRatePenalty
	}
	if policy.P95LatencyMs > learnedLatencyThreshold {
		blended -= learnedLatencyPenalty
	}

	blended = math.Round(blended*100) / 100
	why := fmt.Sprintf("hylinucb(ucb=%.3f, weight=%.2f, n=%d)",
		ucbScore, weight, policy.NSamples)
	return blended, why
}

// ─── HyLinUCB Instance (session-scoped) ───────────────────────────────────

// hyLinUCBInstance wraps the domain HyLinUCB with a lightweight in-memory
// lifecycle. One instance per context signature.
type hyLinUCBInstance struct {
	mu   sync.RWMutex
	arms map[string]*hyLinUCBArm
}

type hyLinUCBArm struct {
	// Diagonal approximation of precision matrices for efficiency.
	aDiag []float64 // d-dim: arm precision diagonal
	bVec  []float64 // d-dim: arm reward accumulator
	n     int64
}

func newHyLinUCBInstance() *hyLinUCBInstance {
	return &hyLinUCBInstance{arms: make(map[string]*hyLinUCBArm)}
}

func (h *hyLinUCBInstance) score(toolID string, x, z []float64) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	arm, ok := h.arms[toolID]
	if !ok {
		// Unknown arm: return base + exploration bonus.
		return 0.5 + hylinucbAlpha
	}

	// Diagonal LinUCB: theta_a = b_a / a_a (element-wise).
	reward := 0.0
	confidenceBound := 0.0
	for i := 0; i < len(x) && i < len(arm.aDiag); i++ {
		if arm.aDiag[i] > 0 {
			theta := arm.bVec[i] / arm.aDiag[i]
			reward += x[i] * theta
			confidenceBound += (x[i] * x[i]) / arm.aDiag[i]
		}
	}

	// Exploration bonus.
	return math.Max(0, math.Min(1, reward+hylinucbAlpha*math.Sqrt(confidenceBound)))
}

func (h *hyLinUCBInstance) update(toolID string, x []float64, reward float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	arm, ok := h.arms[toolID]
	if !ok {
		arm = &hyLinUCBArm{
			aDiag: make([]float64, len(x)),
			bVec:  make([]float64, len(x)),
		}
		// Initialize diagonal with 1 (identity).
		for i := range arm.aDiag {
			arm.aDiag[i] = 1.0
		}
		h.arms[toolID] = arm
	}

	// Diagonal update: A += x * x^T (diagonal only), b += x * reward.
	for i := 0; i < len(x) && i < len(arm.aDiag); i++ {
		arm.aDiag[i] += x[i] * x[i]
		arm.bVec[i] += x[i] * reward
	}
	arm.n++
}

// ─── HyLinUCB Manager ─────────────────────────────────────────────────────

// HyLinUCBManager holds per-context HyLinUCB instances. Thread-safe.
type HyLinUCBManager struct {
	mu        sync.RWMutex
	instances map[string]*hyLinUCBInstance
}

// NewHyLinUCBManager creates a manager for session-scoped HyLinUCB instances.
func NewHyLinUCBManager() *HyLinUCBManager {
	return &HyLinUCBManager{instances: make(map[string]*hyLinUCBInstance)}
}

// GetScorer returns a HyLinUCBScorer for the given context signature.
func (m *HyLinUCBManager) GetScorer(contextSig string) HyLinUCBScorer {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[contextSig]
	if !ok {
		inst = newHyLinUCBInstance()
		m.instances[contextSig] = inst
	}
	return HyLinUCBScorer{instance: inst}
}

// Update feeds an invocation outcome to the HyLinUCB instance.
func (m *HyLinUCBManager) Update(contextSig, toolID string, policy ToolPolicy, success bool) {
	m.mu.RLock()
	inst, ok := m.instances[contextSig]
	m.mu.RUnlock()
	if !ok {
		return
	}

	x := policyToArmFeatures(policy)
	reward := 0.0
	if success {
		reward = 1.0
	}
	inst.update(toolID, x, reward)
}

// ─── Feature Encoding ─────────────────────────────────────────────────────

// policyToArmFeatures encodes a ToolPolicy into a 13-dim arm-specific
// context feature vector. Mirrors tool-learning/domain/features.go
// ContextFeatures layout.
func policyToArmFeatures(p ToolPolicy) []float64 {
	f := make([]float64, hylinucbArmDim)

	// Confidence as primary signal.
	f[0] = p.Confidence

	// Error rate.
	f[1] = p.ErrorRate

	// Latency (log scale, normalized).
	if p.P95LatencyMs > 0 {
		f[2] = math.Min(math.Log1p(float64(p.P95LatencyMs))/10.0, 1.0)
	}

	// Sample size (log scale).
	if p.NSamples > 0 {
		f[3] = math.Min(math.Log1p(float64(p.NSamples))/10.0, 1.0)
	}

	// Alpha/Beta ratio (Thompson posterior shape).
	if p.Alpha+p.Beta > 0 {
		f[4] = p.Alpha / (p.Alpha + p.Beta)
	}

	// Cost (log scale).
	if p.P95Cost > 0 {
		f[5] = math.Min(math.Log1p(p.P95Cost)/5.0, 1.0)
	}

	// Context signature encoding (language family).
	sig := p.ContextSignature
	parts := strings.SplitN(sig, ":", 3)
	if len(parts) >= 2 {
		switch parts[1] {
		case "go":
			f[6] = 1
		case "python":
			f[7] = 1
		case "javascript":
			f[8] = 1
		case "rust":
			f[9] = 1
		default:
			f[10] = 1
		}
	}
	// Task family.
	if len(parts) >= 1 {
		switch parts[0] {
		case "io", "vcs":
			f[11] = 1
		case "build", "test":
			f[12] = 1
		}
	}

	return f
}

// policyToSharedFeatures encodes a ToolPolicy into a 17-dim shared feature
// vector (context + arm metadata concatenated).
func policyToSharedFeatures(p ToolPolicy) []float64 {
	arm := policyToArmFeatures(p)
	// Pad with 4 tool-level features (risk, side_effects, cost, approval).
	// These are not available from ToolPolicy alone, so use policy-derived proxies.
	toolFeatures := make([]float64, 4)
	// Error rate proxy for risk.
	toolFeatures[0] = math.Min(p.ErrorRate*2, 1.0)
	// Cost proxy.
	if p.P95Cost > 0 {
		toolFeatures[1] = math.Min(p.P95Cost/10.0, 1.0)
	}
	// Confidence inverse as approval proxy.
	toolFeatures[2] = 1.0 - p.Confidence
	// Latency proxy for side effects.
	if p.P95LatencyMs > 1000 {
		toolFeatures[3] = 1.0
	}

	z := make([]float64, hylinucbSharedDim)
	copy(z, arm)
	copy(z[hylinucbArmDim:], toolFeatures)
	return z
}

// ─── Capability-based feature encoding ────────────────────────────────────

// capabilityToArmFeatures encodes tool capability metadata for HyLinUCB
// arm features when ToolPolicy data is unavailable.
func capabilityToArmFeatures(cap domain.Capability) []float64 {
	f := make([]float64, hylinucbArmDim)
	switch cap.RiskLevel {
	case domain.RiskLow:
		f[0] = 0.8 // high confidence for low-risk
	case domain.RiskMedium:
		f[0] = 0.5
	case domain.RiskHigh:
		f[0] = 0.2
	}
	return f
}
