package app

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

const (
	hylinucbAlgorithmID = "hylinucb_hybrid"
	hylinucbVersion     = "1.0.0"
	hylinucbArmDim      = 13
	hylinucbAlpha       = 0.25
)

// HyLinUCBScorer implements RecommendationScorer using a diagonal
// approximation of Hybrid Linear UCB (per-arm diagonal precision). It
// maintains mutable state (diagonal precision accumulation) per context
// signature, so it is session-scoped: learning resets across process
// restarts but explores efficiently within a process lifetime.
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

	// UCB score with exploration bonus.
	ucbScore := s.instance.score(policy.ToolID, x)

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

func (h *hyLinUCBInstance) score(toolID string, x []float64) float64 {
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

	// Policy-derived scalar features [0-5], shared with the neural encoder.
	encodePolicyScalarFeatures(f, p)

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
