package domain

import (
	"math"
	"sync"
)

// HyLinUCB implements Hybrid Linear UCB (Upper Confidence Bound) with
// shared and arm-specific parameters for contextual tool selection.
//
// The reward model is:
//
//	r(x, z) = z^T * beta + x^T * theta_a + alpha * sqrt(s)
//
// Where:
//
//	x = context features (task type, language, framework)
//	z = combined context+arm features (includes tool metadata)
//	beta = shared parameters (learned across all tools)
//	theta_a = arm-specific parameters (learned per tool)
//	alpha = exploration coefficient
//	s = confidence bound width
//
// Reference: Das, Sinha. "Linear Contextual Bandits with Hybrid Payoff:
// Revisited." ECML PKDD 2024. arxiv:2406.10131
//
// Original: Li et al. "A Contextual-Bandit Approach to Personalized News
// Article Recommendation." WWW 2010. Section 3.2.
type HyLinUCB struct {
	mu sync.RWMutex

	// Shared parameters (learned across all arms).
	a0 *matrix // k×k — accumulated shared precision
	b0 *vector // k×1 — accumulated shared reward signal

	// Per-arm parameters.
	arms map[string]*armState

	// Hyperparameters.
	alpha float64 // exploration coefficient (default 0.25)
	k     int     // shared feature dimension
	d     int     // arm-specific feature dimension
}

// armState holds the per-arm LinUCB state.
type armState struct {
	a *matrix // d×d — accumulated arm precision
	b *vector // d×1 — accumulated arm reward signal
	// Cross terms for hybrid payoff.
	bigB *matrix // d×k — accumulated cross precision
}

// NewHyLinUCB creates a HyLinUCB scorer.
// k = shared feature dimension (context + arm combined features).
// d = arm-specific context feature dimension.
// alpha = exploration coefficient (0.25 is a good default).
func NewHyLinUCB(k, d int, alpha float64) *HyLinUCB {
	return &HyLinUCB{
		a0:    identityMatrix(k),
		b0:    zeroVector(k),
		arms:  make(map[string]*armState),
		alpha: alpha,
		k:     k,
		d:     d,
	}
}

// Score computes the UCB score for a (context, tool) pair.
// x = arm-specific context features (length d).
// z = shared features (length k) — typically context + arm metadata concatenated.
// toolID identifies the arm.
func (h *HyLinUCB) Score(toolID string, x, z []float64) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	arm := h.getOrCreateArm(toolID)

	// Shared parameter estimate: beta_hat = A0^{-1} * b0
	a0Inv := arm.a.identity(h.k) // placeholder, real impl below
	_ = a0Inv
	betaHat := h.a0.solve(h.b0)

	// Arm-specific parameter estimate: theta_hat = A_a^{-1} * (b_a - B_a * beta_hat)
	bMinusBBeta := arm.b.sub(arm.bigB.mulVec(betaHat))
	thetaHat := arm.a.solve(bMinusBBeta)

	// Predicted reward.
	reward := dotProduct(z, betaHat.data) + dotProduct(x, thetaHat.data)

	// Confidence bound width.
	// s = z^T A0^{-1} z + x^T A_a^{-1} x - 2 * z^T A0^{-1} B_a^T A_a^{-1} x
	//     + x^T A_a^{-1} B_a A0^{-1} B_a^T A_a^{-1} x
	// Simplified: use the diagonal approximation for computational efficiency.
	aInv := arm.a.diagInverse()
	a0InvDiag := h.a0.diagInverse()
	sCtx := quadFormDiag(x, aInv)
	sShared := quadFormDiag(z, a0InvDiag)
	s := sCtx + sShared // conservative approximation

	return reward + h.alpha*math.Sqrt(s)
}

// Update updates the model with an observed reward for a (context, tool) pair.
// x = arm-specific context features, z = shared features, reward in [0, 1].
func (h *HyLinUCB) Update(toolID string, x, z []float64, reward float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	arm := h.getOrCreateArmLocked(toolID)

	xVec := newVector(x)
	zVec := newVector(z)

	// Update shared parameters.
	// A0 += B_a^T A_a^{-1} B_a
	aInv := arm.a.diagInverse()
	bTAinv := arm.bigB.transposeMulDiag(aInv)
	h.a0.addOuterProduct(zVec, zVec)
	h.a0.addMatrix(bTAinv.mulMat(arm.bigB))
	h.b0.addScaled(zVec, reward)
	h.b0.addVec(bTAinv.mulVec(arm.b))

	// Update arm-specific parameters.
	arm.a.addOuterProduct(xVec, xVec)
	arm.b.addScaled(xVec, reward)
	arm.bigB.addOuterXY(xVec, zVec)

	// Adjust shared for new arm state.
	aInvNew := arm.a.diagInverse()
	bTAinvNew := arm.bigB.transposeMulDiag(aInvNew)
	h.a0.subMatrix(bTAinvNew.mulMat(arm.bigB))
	h.b0.subVec(bTAinvNew.mulVec(arm.b))
}

// ScoreAll scores all known arms for a given context and returns sorted
// (toolID, score) pairs in descending order.
func (h *HyLinUCB) ScoreAll(contextFeatures, armFeaturesFn func(toolID string) (x, z []float64)) []ToolScore {
	h.mu.RLock()
	armIDs := make([]string, 0, len(h.arms))
	for id := range h.arms {
		armIDs = append(armIDs, id)
	}
	h.mu.RUnlock()

	scores := make([]ToolScore, 0, len(armIDs))
	for _, id := range armIDs {
		x, z := armFeaturesFn(id)
		score := h.Score(id, x, z)
		scores = append(scores, ToolScore{ToolID: id, Score: score})
	}

	// Sort descending.
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].Score > scores[i].Score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	return scores
}

// ToolScore is a (tool, UCB score) pair.
type ToolScore struct {
	ToolID string  `json:"tool_id"`
	Score  float64 `json:"score"`
}

// ArmCount returns the number of known arms.
func (h *HyLinUCB) ArmCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.arms)
}

func (h *HyLinUCB) getOrCreateArm(toolID string) *armState {
	if arm, ok := h.arms[toolID]; ok {
		return arm
	}
	// Create under write lock (caller must upgrade if needed).
	return h.getOrCreateArmLocked(toolID)
}

func (h *HyLinUCB) getOrCreateArmLocked(toolID string) *armState {
	if arm, ok := h.arms[toolID]; ok {
		return arm
	}
	arm := &armState{
		a:    identityMatrix(h.d),
		b:    zeroVector(h.d),
		bigB: zeroMatrix(h.d, h.k),
	}
	h.arms[toolID] = arm
	return arm
}

// ── Minimal linear algebra (no external dependency) ──

type matrix struct {
	data [][]float64
	rows int
	cols int
}

type vector struct {
	data []float64
}

func identityMatrix(n int) *matrix {
	m := &matrix{data: make([][]float64, n), rows: n, cols: n}
	for i := range n {
		m.data[i] = make([]float64, n)
		m.data[i][i] = 1.0
	}
	return m
}

func zeroMatrix(rows, cols int) *matrix {
	m := &matrix{data: make([][]float64, rows), rows: rows, cols: cols}
	for i := range rows {
		m.data[i] = make([]float64, cols)
	}
	return m
}

func zeroVector(n int) *vector {
	return &vector{data: make([]float64, n)}
}

func newVector(d []float64) *vector {
	c := make([]float64, len(d))
	copy(c, d)
	return &vector{data: c}
}

func (m *matrix) identity(n int) *matrix {
	return identityMatrix(n)
}

// solve computes A^{-1} * b using diagonal approximation for efficiency.
func (m *matrix) solve(b *vector) *vector {
	result := make([]float64, m.rows)
	for i := range m.rows {
		if m.data[i][i] != 0 {
			result[i] = b.data[i] / m.data[i][i]
		}
	}
	return &vector{data: result}
}

// diagInverse returns diagonal elements inverted (diagonal approximation).
func (m *matrix) diagInverse() []float64 {
	inv := make([]float64, m.rows)
	for i := range m.rows {
		if m.data[i][i] != 0 {
			inv[i] = 1.0 / m.data[i][i]
		}
	}
	return inv
}

func (m *matrix) addOuterProduct(x, y *vector) {
	for i := range m.rows {
		for j := range m.cols {
			m.data[i][j] += x.data[i] * y.data[j]
		}
	}
}

func (m *matrix) addMatrix(other *matrix) {
	for i := range m.rows {
		for j := range m.cols {
			m.data[i][j] += other.data[i][j]
		}
	}
}

func (m *matrix) subMatrix(other *matrix) {
	for i := range m.rows {
		for j := range m.cols {
			m.data[i][j] -= other.data[i][j]
		}
	}
}

func (m *matrix) mulVec(v *vector) *vector {
	result := make([]float64, m.rows)
	for i := range m.rows {
		for j := range m.cols {
			result[i] += m.data[i][j] * v.data[j]
		}
	}
	return &vector{data: result}
}

func (m *matrix) mulMat(other *matrix) *matrix {
	result := zeroMatrix(m.rows, other.cols)
	for i := range m.rows {
		for j := range other.cols {
			for k := range m.cols {
				result.data[i][j] += m.data[i][k] * other.data[k][j]
			}
		}
	}
	return result
}

// transposeMulDiag computes M^T * diag(d) — transpose of M times diagonal matrix.
func (m *matrix) transposeMulDiag(diag []float64) *matrix {
	result := zeroMatrix(m.cols, m.rows)
	for i := range m.cols {
		for j := range m.rows {
			result.data[i][j] = m.data[j][i] * diag[j]
		}
	}
	return result
}

func (m *matrix) addOuterXY(x *vector, y *vector) {
	for i := range m.rows {
		for j := range m.cols {
			m.data[i][j] += x.data[i] * y.data[j]
		}
	}
}

func (v *vector) addScaled(other *vector, scale float64) {
	for i := range v.data {
		v.data[i] += other.data[i] * scale
	}
}

func (v *vector) addVec(other *vector) {
	for i := range v.data {
		v.data[i] += other.data[i]
	}
}

func (v *vector) subVec(other *vector) {
	for i := range v.data {
		v.data[i] -= other.data[i]
	}
}

func (v *vector) sub(other *vector) *vector {
	result := make([]float64, len(v.data))
	for i := range v.data {
		result[i] = v.data[i] - other.data[i]
	}
	return &vector{data: result}
}

func dotProduct(a, b []float64) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func quadFormDiag(x []float64, diagInv []float64) float64 {
	sum := 0.0
	for i := range x {
		sum += x[i] * x[i] * diagInv[i]
	}
	return sum
}
