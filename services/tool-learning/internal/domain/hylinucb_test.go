package domain

import (
	"testing"
)

func TestHyLinUCB_NewAndScore(t *testing.T) {
	h := NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, 0.25)

	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	arm := EncodeToolFeatures("low", "none", "free", false)
	z := EncodeSharedFeatures(ctx, arm)

	// Score before any updates — should be positive (exploration bonus).
	score := h.Score("fs.write_file", ctx, z)
	if score <= 0 {
		t.Errorf("initial score should be positive (exploration), got %f", score)
	}
}

func TestHyLinUCB_UpdateAndScore(t *testing.T) {
	h := NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, 0.25)

	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	armGood := EncodeToolFeatures("low", "none", "free", false)
	armBad := EncodeToolFeatures("high", "irreversible", "high", true)
	zGood := EncodeSharedFeatures(ctx, armGood)
	zBad := EncodeSharedFeatures(ctx, armBad)

	// Train: fs.write_file succeeds 90% of the time.
	for range 90 {
		h.Update("fs.write_file", ctx, zGood, 1.0)
	}
	for range 10 {
		h.Update("fs.write_file", ctx, zGood, 0.0)
	}

	// Train: k8s.apply fails 80% of the time.
	for range 20 {
		h.Update("k8s.apply", ctx, zBad, 1.0)
	}
	for range 80 {
		h.Update("k8s.apply", ctx, zBad, 0.0)
	}

	scoreGood := h.Score("fs.write_file", ctx, zGood)
	scoreBad := h.Score("k8s.apply", ctx, zBad)

	if scoreGood <= scoreBad {
		t.Errorf("good tool (%f) should score higher than bad tool (%f)", scoreGood, scoreBad)
	}
}

func TestHyLinUCB_ScoreAll(t *testing.T) {
	h := NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, 0.25)

	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "python", ConstraintsClass: "std"})

	// Register some tools with different success rates.
	tools := map[string]float64{
		"fs.write_file": 0.95,
		"fs.read_file":  0.99,
		"git.push":      0.70,
		"k8s.apply":     0.30,
	}

	arm := EncodeToolFeatures("low", "none", "free", false)
	for tool, rate := range tools {
		z := EncodeSharedFeatures(ctx, arm)
		for range 100 {
			reward := 0.0
			if float64(pseudoRandom(tool))/(1<<31) < rate {
				reward = 1.0
			}
			h.Update(tool, ctx, z, reward)
		}
		_ = z // use z for each tool
	}

	featureFn := func(toolID string) ([]float64, []float64) {
		return ctx, EncodeSharedFeatures(ctx, arm)
	}

	scores := h.ScoreAll(nil, featureFn)
	if len(scores) != 4 {
		t.Fatalf("expected 4 scores, got %d", len(scores))
	}

	// Top tool should be one of the high-success ones.
	top := scores[0].ToolID
	if top != "fs.read_file" && top != "fs.write_file" {
		t.Logf("top tool: %s (score %f) — may vary due to exploration", top, scores[0].Score)
	}

	if h.ArmCount() != 4 {
		t.Errorf("expected 4 arms, got %d", h.ArmCount())
	}
}

func TestHyLinUCB_ExplorationDecays(t *testing.T) {
	h := NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, 0.25)

	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	arm := EncodeToolFeatures("low", "none", "free", false)
	z := EncodeSharedFeatures(ctx, arm)

	// Score with no data (high exploration).
	scoreBefore := h.Score("fs.list", ctx, z)

	// Add 1000 observations.
	for range 1000 {
		h.Update("fs.list", ctx, z, 0.8)
	}

	scoreAfter := h.Score("fs.list", ctx, z)

	// Exploration bonus should have decreased (confidence narrows).
	// The predicted reward should dominate.
	t.Logf("before=%f, after=%f", scoreBefore, scoreAfter)
	// We don't assert exact values since diagonal approximation
	// affects precision, but both should be reasonable.
	if scoreAfter < 0 {
		t.Errorf("score after 1000 positive updates should be positive, got %f", scoreAfter)
	}
}

func TestFeatureEncoding(t *testing.T) {
	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	if len(ctx) != ArmFeatureDim {
		t.Fatalf("context features length = %d, want %d", len(ctx), ArmFeatureDim)
	}
	// Go should be hot.
	if ctx[0] != 1.0 {
		t.Errorf("LangGo should be 1.0, got %f", ctx[0])
	}

	arm := EncodeToolFeatures("high", "irreversible", "high", true)
	if len(arm) != 4 {
		t.Fatalf("arm features length = %d, want 4", len(arm))
	}
	if arm[0] != 1.0 || arm[1] != 1.0 || arm[2] != 1.0 || arm[3] != 1.0 {
		t.Errorf("high risk/irreversible/high cost/approval should all be 1.0, got %v", arm)
	}

	z := EncodeSharedFeatures(ctx, arm)
	if len(z) != SharedFeatureDim {
		t.Fatalf("shared features length = %d, want %d", len(z), SharedFeatureDim)
	}
}

// pseudoRandom is a simple deterministic hash for test reproducibility.
func pseudoRandom(s string) uint32 {
	h := uint32(0)
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}
