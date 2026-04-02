package app

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
)

// ─── MLP (Multi-Layer Perceptron) ──────────────────────────────────────────

const (
	neuralInputDim  = 17 // 13 context features + 4 tool features
	neuralHiddenDim = 32
	neuralOutputDim = 1
)

// MLPWeights holds the parameters of a 2-layer MLP: input→hidden→output.
// Stored as flat slices for efficient serialization to/from Valkey.
type MLPWeights struct {
	W1 []float64 `json:"w1"` // [inputDim × hiddenDim] row-major
	B1 []float64 `json:"b1"` // [hiddenDim]
	W2 []float64 `json:"w2"` // [hiddenDim × outputDim] row-major
	B2 []float64 `json:"b2"` // [outputDim]
}

// ParamCount returns the total number of parameters.
func (m *MLPWeights) ParamCount() int {
	return len(m.W1) + len(m.B1) + len(m.W2) + len(m.B2)
}

// Valid checks that all weight dimensions are correct.
func (m *MLPWeights) Valid() bool {
	return len(m.W1) == neuralInputDim*neuralHiddenDim &&
		len(m.B1) == neuralHiddenDim &&
		len(m.W2) == neuralHiddenDim*neuralOutputDim &&
		len(m.B2) == neuralOutputDim
}

// Forward computes the MLP output: ReLU(x·W1 + b1)·W2 + b2.
// Input x must have length neuralInputDim. Returns a scalar.
func (m *MLPWeights) Forward(x []float64) float64 {
	if len(x) != neuralInputDim {
		return 0
	}

	// Hidden layer: z = ReLU(x·W1 + b1)
	hidden := make([]float64, neuralHiddenDim)
	for j := range neuralHiddenDim {
		sum := m.B1[j]
		for i := range neuralInputDim {
			sum += x[i] * m.W1[i*neuralHiddenDim+j]
		}
		if sum > 0 {
			hidden[j] = sum // ReLU
		}
	}

	// Output layer: out = hidden·W2 + b2
	out := m.B2[0]
	for j := range neuralHiddenDim {
		out += hidden[j] * m.W2[j]
	}
	return out
}

// ForwardPerturbed runs a forward pass with Gaussian noise added to the
// last-layer weights (W2). This is the NeuralTS exploration mechanism:
// σ controls the perturbation magnitude — higher σ means more exploration.
func (m *MLPWeights) ForwardPerturbed(x []float64, sigma float64) float64 {
	if len(x) != neuralInputDim {
		return 0
	}

	// Hidden layer (deterministic).
	hidden := make([]float64, neuralHiddenDim)
	for j := range neuralHiddenDim {
		sum := m.B1[j]
		for i := range neuralInputDim {
			sum += x[i] * m.W1[i*neuralHiddenDim+j]
		}
		if sum > 0 {
			hidden[j] = sum
		}
	}

	// Output layer with perturbed weights.
	out := m.B2[0] + sigma*rand.NormFloat64()
	for j := range neuralHiddenDim {
		perturbedW := m.W2[j] + sigma*rand.NormFloat64()
		out += hidden[j] * perturbedW
	}
	return out
}

// NewRandomMLPWeights initializes weights with Xavier/Glorot initialization.
func NewRandomMLPWeights() *MLPWeights {
	m := &MLPWeights{
		W1: make([]float64, neuralInputDim*neuralHiddenDim),
		B1: make([]float64, neuralHiddenDim),
		W2: make([]float64, neuralHiddenDim*neuralOutputDim),
		B2: make([]float64, neuralOutputDim),
	}

	// Xavier init for W1: N(0, 2/(fan_in+fan_out))
	scale1 := math.Sqrt(2.0 / float64(neuralInputDim+neuralHiddenDim))
	for i := range m.W1 {
		m.W1[i] = rand.NormFloat64() * scale1
	}

	// Xavier init for W2.
	scale2 := math.Sqrt(2.0 / float64(neuralHiddenDim+neuralOutputDim))
	for i := range m.W2 {
		m.W2[i] = rand.NormFloat64() * scale2
	}

	return m
}

// MarshalJSON serializes weights to JSON for Valkey storage.
func (m *MLPWeights) MarshalJSON() ([]byte, error) {
	type alias MLPWeights
	return json.Marshal((*alias)(m))
}

// UnmarshalMLPWeights deserializes weights from JSON (e.g., from Valkey).
func UnmarshalMLPWeights(data []byte) (*MLPWeights, error) {
	var m MLPWeights
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal MLP weights: %w", err)
	}
	if !m.Valid() {
		return nil, fmt.Errorf("invalid MLP dimensions: W1=%d B1=%d W2=%d B2=%d",
			len(m.W1), len(m.B1), len(m.W2), len(m.B2))
	}
	return &m, nil
}

// ─── NeuralTS Scorer ───────────────────────────────────────────────────────

// NeuralTSScorer implements RecommendationScorer using Neural Thompson
// Sampling. It runs a forward pass with perturbed last-layer weights for
// exploration. The perturbation magnitude decreases as n_samples grows
// (less exploration when more data is available).
type NeuralTSScorer struct {
	Model *MLPWeights
}

const (
	neuralTSAlgorithmID = "neural_thompson_sampling"
	neuralTSVersion     = "1.0.0"
)

func (s NeuralTSScorer) AlgorithmID() string      { return neuralTSAlgorithmID }
func (s NeuralTSScorer) AlgorithmVersion() string { return neuralTSVersion }

func (s NeuralTSScorer) Score(base float64, policy ToolPolicy) (float64, string) {
	if s.Model == nil || !s.Model.Valid() {
		return base, ""
	}
	if policy.NSamples < learnedMinSamples {
		return base, ""
	}

	// Encode policy as feature vector.
	features := policyToFeatures(policy)

	// Exploration magnitude: σ = 1/√n — decays as data grows.
	sigma := 1.0 / math.Sqrt(float64(policy.NSamples))

	// NeuralTS: forward pass with perturbed weights.
	rawScore := s.Model.ForwardPerturbed(features, sigma)

	// Sigmoid to [0, 1] then blend with heuristic base.
	neuralProb := sigmoid(rawScore)

	// Blend: trust neural more as n grows.
	weight := math.Min(float64(policy.NSamples)/200.0, 0.9)
	blended := (1-weight)*base + weight*neuralProb

	// SLO penalties.
	if policy.ErrorRate > learnedErrorRateThreshold {
		blended -= learnedErrorRatePenalty
	}
	if policy.P95LatencyMs > learnedLatencyThreshold {
		blended -= learnedLatencyPenalty
	}

	blended = math.Round(blended*100) / 100
	why := fmt.Sprintf("neural_ts(prob=%.3f, σ=%.3f, weight=%.2f, n=%d)",
		neuralProb, sigma, weight, policy.NSamples)
	return blended, why
}

// policyToFeatures encodes a ToolPolicy into the 17-dim feature vector
// expected by the MLP. This mirrors the encoding in tool-learning/features.go.
func policyToFeatures(p ToolPolicy) []float64 {
	f := make([]float64, neuralInputDim)

	// Confidence as primary signal (0-1).
	f[0] = p.Confidence

	// Error rate (0-1).
	f[1] = p.ErrorRate

	// Normalized latency (log scale, capped).
	if p.P95LatencyMs > 0 {
		f[2] = math.Min(math.Log1p(float64(p.P95LatencyMs))/10.0, 1.0)
	}

	// Sample size (log scale, normalized).
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

	// Freshness: how recent the policy is (decay).
	// Remaining features [6-16] are reserved for context encoding
	// (language, task family, constraints) — populated when full
	// context features are available from the workspace digest.

	return f
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

// NeuralModelValkeyKey is the Valkey key where the trained model is stored.
const NeuralModelValkeyKey = "neural_ts:model:v1"
