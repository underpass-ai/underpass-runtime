package app

import (
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestScoreTool_LowRiskNoSideEffects(t *testing.T) {
	cap := &domain.Capability{
		Name:        "fs.read_file",
		Description: "Read file contents",
		RiskLevel:   domain.RiskLow,
		SideEffects: domain.SideEffectsNone,
		Constraints: domain.Constraints{TimeoutSeconds: 5},
	}
	rec := scoreTool(cap, nil)
	if rec.Score != 1.0 {
		t.Fatalf("expected score 1.0 for low-risk/no-side-effects, got %v", rec.Score)
	}
	if rec.EstimatedCost != "cheap" {
		t.Fatalf("expected cost cheap, got %s", rec.EstimatedCost)
	}
	if rec.Name != "fs.read_file" {
		t.Fatalf("expected name fs.read_file, got %s", rec.Name)
	}
}

func TestScoreTool_HighRiskIrreversible(t *testing.T) {
	cap := &domain.Capability{
		Name:             "k8s.delete_pod",
		Description:      "Delete a Kubernetes pod",
		RiskLevel:        domain.RiskHigh,
		SideEffects:      domain.SideEffectsIrreversible,
		RequiresApproval: true,
		Constraints:      domain.Constraints{TimeoutSeconds: 30},
	}
	rec := scoreTool(cap, nil)
	expected := baseScore - riskPenaltyHigh - sideEffectPenIrr - approvalPenalty - costPenMedium
	if rec.Score != expected {
		t.Fatalf("expected score %v, got %v", expected, rec.Score)
	}
}

func TestScoreTool_MediumRisk(t *testing.T) {
	cap := &domain.Capability{
		Name:        "git.commit",
		Description: "Create a git commit",
		RiskLevel:   domain.RiskMedium,
		SideEffects: domain.SideEffectsReversible,
		Constraints: domain.Constraints{TimeoutSeconds: 10},
	}
	rec := scoreTool(cap, nil)
	expected := baseScore - riskPenaltyMed - sideEffectPenRev
	if rec.Score != expected {
		t.Fatalf("expected score %v, got %v", expected, rec.Score)
	}
}

func TestScoreTool_ExpensiveCost(t *testing.T) {
	cap := &domain.Capability{
		Name:        "repo.build",
		Description: "Build project",
		RiskLevel:   domain.RiskLow,
		SideEffects: domain.SideEffectsNone,
		Constraints: domain.Constraints{TimeoutSeconds: 120},
	}
	rec := scoreTool(cap, nil)
	expected := baseScore - costPenExpensive
	if rec.Score != expected {
		t.Fatalf("expected score %v, got %v", expected, rec.Score)
	}
	if rec.EstimatedCost != "expensive" {
		t.Fatalf("expected cost expensive, got %s", rec.EstimatedCost)
	}
}

func TestScoreTool_HintMatchBonus(t *testing.T) {
	cap := &domain.Capability{
		Name:        "repo.test",
		Description: "Run unit tests for the project",
		RiskLevel:   domain.RiskLow,
		SideEffects: domain.SideEffectsNone,
		Constraints: domain.Constraints{TimeoutSeconds: 60},
	}
	noHint := scoreTool(cap, nil)
	withHint := scoreTool(cap, tokenize("run unit tests"))

	if withHint.Score <= noHint.Score {
		t.Fatalf("hint match should increase score: with=%v without=%v", withHint.Score, noHint.Score)
	}
}

func TestScoreTool_HintPartialMatch(t *testing.T) {
	cap := &domain.Capability{
		Name:        "fs.read_file",
		Description: "Read file contents from workspace",
		RiskLevel:   domain.RiskLow,
		SideEffects: domain.SideEffectsNone,
		Constraints: domain.Constraints{TimeoutSeconds: 5},
	}
	// "read" matches, "deploy" does not
	rec := scoreTool(cap, tokenize("read deploy"))
	if rec.Score <= 1.0 {
		t.Fatalf("partial hint match should add some bonus: got %v", rec.Score)
	}
	if rec.Score >= 1.0+hintMatchBonus {
		t.Fatalf("partial match should add less than full bonus: got %v", rec.Score)
	}
}

func TestScoreTool_PolicyNotesEmpty(t *testing.T) {
	cap := &domain.Capability{Name: "fs.list", RiskLevel: domain.RiskLow, SideEffects: domain.SideEffectsNone}
	rec := scoreTool(cap, nil)
	if rec.PolicyNotes == nil {
		t.Fatal("policy_notes should be non-nil empty slice")
	}
	if len(rec.PolicyNotes) != 0 {
		t.Fatalf("expected empty policy_notes, got %v", rec.PolicyNotes)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"run unit tests", 3},
		{"a", 0},                    // single char filtered
		{"read the file", 3},        // "the" kept (len>=2)
		{"  spaces  everywhere  ", 2}, // trimmed
		{"punctuation! marks?", 2},
	}
	for _, tt := range tests {
		tokens := tokenize(tt.input)
		if len(tokens) != tt.expected {
			t.Errorf("tokenize(%q): expected %d tokens, got %d: %v", tt.input, tt.expected, len(tokens), tokens)
		}
	}
}

func TestCountHintMatches(t *testing.T) {
	cap := &domain.Capability{
		Name:        "repo.test",
		Description: "Run unit tests for the project",
	}
	if count := countHintMatches(cap, tokenize("run test")); count != 2 {
		t.Fatalf("expected 2 matches, got %d", count)
	}
	if count := countHintMatches(cap, tokenize("deploy kubernetes")); count != 0 {
		t.Fatalf("expected 0 matches, got %d", count)
	}
	if count := countHintMatches(cap, tokenize("repo")); count != 1 {
		t.Fatalf("expected 1 match (name), got %d", count)
	}
}
