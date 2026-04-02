package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
)

// NeuralTrainer trains a small MLP (17→32→1) from aggregate telemetry
// using mini-batch SGD. The trained weights are serialized to JSON for
// storage in Valkey, where the runtime loads them for NeuralTS scoring.

const (
	neuralInputDim  = 17
	neuralHiddenDim = 32
)

// NeuralMLPWeights mirrors the runtime's MLPWeights for serialization compatibility.
type NeuralMLPWeights struct {
	W1 []float64 `json:"w1"`
	B1 []float64 `json:"b1"`
	W2 []float64 `json:"w2"`
	B2 []float64 `json:"b2"`
}

// TrainingSample is one (features, reward) pair for supervised learning.
type TrainingSample struct {
	Features []float64
	Reward   float64 // 0 or 1 (binary: success/fail)
}

// NeuralTrainerConfig holds hyperparameters.
type NeuralTrainerConfig struct {
	LearningRate float64
	Epochs       int
	BatchSize    int
}

// DefaultNeuralTrainerConfig returns sensible defaults.
func DefaultNeuralTrainerConfig() NeuralTrainerConfig {
	return NeuralTrainerConfig{
		LearningRate: 0.01,
		Epochs:       50,
		BatchSize:    32,
	}
}

// TrainNeuralModel trains a 2-layer MLP from aggregate stats and returns
// serialized weights ready for Valkey storage.
func TrainNeuralModel(samples []TrainingSample, cfg NeuralTrainerConfig) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no training samples")
	}

	// Initialize weights.
	w := initWeights()

	// Mini-batch SGD.
	for epoch := range cfg.Epochs {
		// Shuffle samples.
		shuffled := make([]TrainingSample, len(samples))
		copy(shuffled, samples)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		totalLoss := 0.0
		batches := 0
		for i := 0; i < len(shuffled); i += cfg.BatchSize {
			end := i + cfg.BatchSize
			if end > len(shuffled) {
				end = len(shuffled)
			}
			batch := shuffled[i:end]
			loss := trainBatch(w, batch, cfg.LearningRate)
			totalLoss += loss
			batches++
		}

		_ = epoch // suppress unused warning in production
		// Optionally log: epoch, totalLoss/float64(batches)
	}

	return json.Marshal(w)
}

// AggregatesToSamples converts AggregateStats into training samples.
// Each aggregate becomes one sample with reward = success_rate.
func AggregatesToSamples(aggregates []AggregateStats) []TrainingSample {
	samples := make([]TrainingSample, 0, len(aggregates))
	for _, agg := range aggregates {
		features := encodeAggregate(agg)
		reward := 0.0
		if agg.Total > 0 {
			reward = float64(agg.Successes) / float64(agg.Total)
		}
		samples = append(samples, TrainingSample{Features: features, Reward: reward})
	}
	return samples
}

func encodeAggregate(agg AggregateStats) []float64 {
	f := make([]float64, neuralInputDim)

	// Success rate as primary signal.
	if agg.Total > 0 {
		f[0] = float64(agg.Successes) / float64(agg.Total)
	}
	f[1] = agg.ErrorRate
	if agg.P95LatencyMs > 0 {
		f[2] = math.Min(math.Log1p(float64(agg.P95LatencyMs))/10.0, 1.0)
	}
	if agg.Total > 0 {
		f[3] = math.Min(math.Log1p(float64(agg.Total))/10.0, 1.0)
	}
	f[4] = agg.P95Cost
	// Remaining features reserved for context encoding.
	return f
}

// ─── Internal training helpers ─────────────────────────────────────────────

func initWeights() *NeuralMLPWeights {
	w := &NeuralMLPWeights{
		W1: make([]float64, neuralInputDim*neuralHiddenDim),
		B1: make([]float64, neuralHiddenDim),
		W2: make([]float64, neuralHiddenDim),
		B2: make([]float64, 1),
	}
	scale1 := math.Sqrt(2.0 / float64(neuralInputDim+neuralHiddenDim))
	for i := range w.W1 {
		w.W1[i] = rand.NormFloat64() * scale1
	}
	scale2 := math.Sqrt(2.0 / float64(neuralHiddenDim+1))
	for i := range w.W2 {
		w.W2[i] = rand.NormFloat64() * scale2
	}
	return w
}

func forward(w *NeuralMLPWeights, x []float64) (hidden []float64, out float64) {
	hidden = make([]float64, neuralHiddenDim)
	for j := range neuralHiddenDim {
		sum := w.B1[j]
		for i := range neuralInputDim {
			sum += x[i] * w.W1[i*neuralHiddenDim+j]
		}
		if sum > 0 {
			hidden[j] = sum
		}
	}
	out = w.B2[0]
	for j := range neuralHiddenDim {
		out += hidden[j] * w.W2[j]
	}
	return hidden, out
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

// trainBatch performs one SGD step on a mini-batch. Returns average loss.
func trainBatch(w *NeuralMLPWeights, batch []TrainingSample, lr float64) float64 {
	// Accumulate gradients.
	gW1 := make([]float64, len(w.W1))
	gB1 := make([]float64, len(w.B1))
	gW2 := make([]float64, len(w.W2))
	gB2 := make([]float64, len(w.B2))

	totalLoss := 0.0
	for _, s := range batch {
		hidden, out := forward(w, s.Features)
		pred := sigmoid(out)
		// Binary cross-entropy loss.
		eps := 1e-8
		totalLoss += -(s.Reward*math.Log(pred+eps) + (1-s.Reward)*math.Log(1-pred+eps))

		// dL/dout = pred - reward (sigmoid cross-entropy gradient).
		dout := pred - s.Reward

		// Gradients for W2, B2.
		gB2[0] += dout
		for j := range neuralHiddenDim {
			gW2[j] += dout * hidden[j]
		}

		// Backprop through ReLU to W1, B1.
		for j := range neuralHiddenDim {
			if hidden[j] <= 0 {
				continue // ReLU gate closed
			}
			dh := dout * w.W2[j]
			gB1[j] += dh
			for i := range neuralInputDim {
				gW1[i*neuralHiddenDim+j] += dh * s.Features[i]
			}
		}
	}

	// Apply gradients (SGD).
	n := float64(len(batch))
	for i := range w.W1 {
		w.W1[i] -= lr * gW1[i] / n
	}
	for i := range w.B1 {
		w.B1[i] -= lr * gB1[i] / n
	}
	for i := range w.W2 {
		w.W2[i] -= lr * gW2[i] / n
	}
	for i := range w.B2 {
		w.B2[i] -= lr * gB2[i] / n
	}

	return totalLoss / n
}
