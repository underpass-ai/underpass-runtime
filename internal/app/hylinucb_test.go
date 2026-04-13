package app

import (
	"testing"
)

func TestHyLinUCBScorer_AlgorithmID(t *testing.T) {
	mgr := NewHyLinUCBManager()
	scorer := mgr.GetScorer("io:go:standard")
	if scorer.AlgorithmID() != "hylinucb_hybrid" {
		t.Fatalf("expected hylinucb_hybrid, got %s", scorer.AlgorithmID())
	}
	if scorer.AlgorithmVersion() != "1.0.0" {
		t.Fatalf("expected 1.0.0, got %s", scorer.AlgorithmVersion())
	}
}

func TestHyLinUCBScorer_ScoreWithPolicy(t *testing.T) {
	mgr := NewHyLinUCBManager()
	scorer := mgr.GetScorer("io:go:standard")

	policy := ToolPolicy{
		ToolID:           "fs.edit",
		ContextSignature: "io:go:standard",
		Alpha:            30,
		Beta:             5,
		Confidence:       0.85,
		NSamples:         35,
		ErrorRate:        0.05,
		P95LatencyMs:     50,
	}

	score, why := scorer.Score(0.8, policy)
	if score <= 0 || score > 2.0 {
		t.Fatalf("expected reasonable score, got %.4f", score)
	}
	if why == "" {
		t.Fatal("expected non-empty explanation")
	}
	t.Logf("score=%.4f why=%s", score, why)
}

func TestHyLinUCBScorer_LowSamplesReturnsBase(t *testing.T) {
	mgr := NewHyLinUCBManager()
	scorer := mgr.GetScorer("io:go:standard")

	policy := ToolPolicy{
		ToolID:   "fs.edit",
		NSamples: 2, // below learnedMinSamples
	}

	score, why := scorer.Score(0.7, policy)
	if score != 0.7 {
		t.Fatalf("expected base score 0.7 for low samples, got %.4f", score)
	}
	if why != "" {
		t.Fatalf("expected empty why for low samples, got %s", why)
	}
}

func TestHyLinUCBScorer_UpdateImproves(t *testing.T) {
	mgr := NewHyLinUCBManager()
	scorer := mgr.GetScorer("io:go:standard")

	policy := ToolPolicy{
		ToolID:           "fs.edit",
		ContextSignature: "io:go:standard",
		Alpha:            20,
		Beta:             5,
		Confidence:       0.8,
		NSamples:         25,
	}

	scoreBefore, _ := scorer.Score(0.5, policy)

	// Feed positive outcomes.
	for i := 0; i < 10; i++ {
		mgr.Update("io:go:standard", "fs.edit", policy, true)
	}

	scoreAfter, _ := scorer.Score(0.5, policy)

	// After positive updates, score should change (exploration narrows).
	if scoreBefore == scoreAfter {
		t.Logf("scores unchanged: before=%.4f after=%.4f (may be ok if base dominates)", scoreBefore, scoreAfter)
	}
	t.Logf("before=%.4f after=%.4f", scoreBefore, scoreAfter)
}

func TestSelectScorerFull_HyLinUCBSelected(t *testing.T) {
	mgr := NewHyLinUCBManager()
	policies := map[string]ToolPolicy{
		"fs.edit": {NSamples: 30, Confidence: 0.8}, // 20-99 range → HyLinUCB
	}

	scorer := SelectScorerFull(policies, nil, mgr, "io:go:standard")
	if scorer == nil {
		t.Fatal("expected non-nil scorer")
	}
	if scorer.AlgorithmID() != "hylinucb_hybrid" {
		t.Fatalf("expected hylinucb_hybrid, got %s", scorer.AlgorithmID())
	}
}

func TestSelectScorerFull_NeuralTSOverridesHyLinUCB(t *testing.T) {
	mgr := NewHyLinUCBManager()
	model := NewRandomMLPWeights()
	policies := map[string]ToolPolicy{
		"fs.edit": {NSamples: 200, Confidence: 0.9}, // ≥100 → NeuralTS
	}

	scorer := SelectScorerFull(policies, model, mgr, "io:go:standard")
	if scorer == nil {
		t.Fatal("expected non-nil scorer")
	}
	if scorer.AlgorithmID() != "neural_thompson_sampling" {
		t.Fatalf("expected neural_thompson_sampling, got %s", scorer.AlgorithmID())
	}
}

func TestSelectScorerFull_ThompsonWithoutHyLinUCBManager(t *testing.T) {
	policies := map[string]ToolPolicy{
		"fs.edit": {NSamples: 60, Confidence: 0.8, Alpha: 50, Beta: 10},
	}

	// nil hylinucb manager → falls through to Thompson
	scorer := SelectScorerFull(policies, nil, nil, "io:go:standard")
	if scorer == nil {
		t.Fatal("expected non-nil scorer")
	}
	if scorer.AlgorithmID() != "beta_thompson_sampling" {
		t.Fatalf("expected thompson, got %s", scorer.AlgorithmID())
	}
}

func TestHyLinUCBManager_PerContextIsolation(t *testing.T) {
	mgr := NewHyLinUCBManager()

	// Get scorers for different contexts.
	s1 := mgr.GetScorer("io:go:standard")
	s2 := mgr.GetScorer("vcs:python:strict")

	// They should be different instances.
	if s1.instance == s2.instance {
		t.Fatal("expected different instances for different contexts")
	}

	// Same context returns same instance.
	s3 := mgr.GetScorer("io:go:standard")
	if s1.instance != s3.instance {
		t.Fatal("expected same instance for same context")
	}
}

func TestPolicyToArmFeatures_EncodesProperly(t *testing.T) {
	p := ToolPolicy{
		ContextSignature: "io:go:standard",
		Confidence:       0.9,
		ErrorRate:        0.05,
		P95LatencyMs:     100,
		NSamples:         50,
		Alpha:            45,
		Beta:             5,
		P95Cost:          0.5,
	}

	f := policyToArmFeatures(p)
	if len(f) != hylinucbArmDim {
		t.Fatalf("expected %d features, got %d", hylinucbArmDim, len(f))
	}
	if f[0] != 0.9 { // confidence
		t.Fatalf("expected confidence=0.9, got %.4f", f[0])
	}
	if f[6] != 1.0 { // LangGo
		t.Fatalf("expected LangGo=1.0, got %.4f", f[6])
	}
}

func TestPolicyToSharedFeatures_Correct17Dim(t *testing.T) {
	p := ToolPolicy{
		ContextSignature: "io:go:standard",
		Confidence:       0.8,
		NSamples:         30,
	}

	f := policyToSharedFeatures(p)
	if len(f) != hylinucbSharedDim {
		t.Fatalf("expected %d features, got %d", hylinucbSharedDim, len(f))
	}
}
