package app

import (
	"context"
	"fmt"
	"testing"
)

func TestBuildCrossAgentInsight_NoData(t *testing.T) {
	insight := BuildCrossAgentInsight("gen:go:std", nil, nil, AlgorithmIDHeuristic)
	if insight.Confidence != "low" {
		t.Fatalf("expected low confidence, got %s", insight.Confidence)
	}
	if insight.TotalInvocations != 0 {
		t.Fatalf("expected 0 invocations, got %d", insight.TotalInvocations)
	}
	if insight.InsightSummary() != "no cross-agent data available" {
		t.Fatalf("unexpected summary: %s", insight.InsightSummary())
	}
}

func TestBuildCrossAgentInsight_MediumConfidence(t *testing.T) {
	stats := map[string]ToolStats{
		"fs.read":  {InvocationN: 15, SuccessRate: 0.9, P50Duration: 30},
		"fs.write": {InvocationN: 10, SuccessRate: 0.8, P50Duration: 50},
	}
	insight := BuildCrossAgentInsight("gen:go:std", stats, nil, AlgorithmIDHeuristic)
	if insight.Confidence != "medium" {
		t.Fatalf("expected medium confidence (25 invocations), got %s", insight.Confidence)
	}
	if insight.TotalInvocations != 25 {
		t.Fatalf("expected 25, got %d", insight.TotalInvocations)
	}
	if insight.ToolCount != 2 {
		t.Fatalf("expected 2 tools, got %d", insight.ToolCount)
	}
}

func TestBuildCrossAgentInsight_HighConfidence(t *testing.T) {
	stats := map[string]ToolStats{
		"fs.read": {InvocationN: 100, SuccessRate: 0.95, P50Duration: 20},
	}
	policies := map[string]ToolPolicy{
		"fs.read": {ToolID: "fs.read", NSamples: 100, Confidence: 0.9},
	}
	insight := BuildCrossAgentInsight("gen:go:std", stats, policies, "beta_thompson_sampling")
	if insight.Confidence != "high" {
		t.Fatalf("expected high confidence, got %s", insight.Confidence)
	}
	if !insight.PolicyActive {
		t.Fatal("expected policy active")
	}
	if insight.AlgorithmTier != "thompson" {
		t.Fatalf("expected thompson tier, got %s", insight.AlgorithmTier)
	}
}

func TestBuildCrossAgentInsight_TopToolsCapped(t *testing.T) {
	stats := map[string]ToolStats{}
	for i := 0; i < 10; i++ {
		stats[fmt.Sprintf("tool.%d", i)] = ToolStats{InvocationN: 10 - i, SuccessRate: 0.8}
	}
	insight := BuildCrossAgentInsight("ctx", stats, nil, AlgorithmIDHeuristic)
	if len(insight.TopTools) != 5 {
		t.Fatalf("expected top 5 tools, got %d", len(insight.TopTools))
	}
	// First should have most invocations
	if insight.TopTools[0].Invocations < insight.TopTools[4].Invocations {
		t.Fatal("expected sorted by invocations descending")
	}
}

func TestRecommendTools_IncludesInsight(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetTelemetry(noopTelemetryRecorder{}, &fakeTelemetryQuerier{
		stats: map[string]ToolStats{
			"fs.read_file": {InvocationN: 30, SuccessRate: 0.95, P50Duration: 25},
		},
	})

	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read", 5)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if resp.Insight == nil {
		t.Fatal("expected insight in response")
	}
	if resp.Insight.TotalInvocations != 30 {
		t.Fatalf("expected 30 invocations in insight, got %d", resp.Insight.TotalInvocations)
	}
	if resp.Insight.Confidence != "medium" {
		t.Fatalf("expected medium confidence, got %s", resp.Insight.Confidence)
	}
}

func TestGetCrossAgentInsight(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetTelemetry(noopTelemetryRecorder{}, &fakeTelemetryQuerier{
		stats: map[string]ToolStats{
			"fs.read_file": {InvocationN: 50, SuccessRate: 0.9},
		},
	})

	insight, svcErr := svc.GetCrossAgentInsight(context.Background(), testSessionID)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if insight.ContextSignature == "" {
		t.Fatal("expected context signature")
	}
	if insight.TotalInvocations != 50 {
		t.Fatalf("expected 50, got %d", insight.TotalInvocations)
	}
}

func TestGetCrossAgentInsight_SessionNotFound(t *testing.T) {
	svc2 := NewService(
		&fakeWorkspaceManager{found: false},
		&fakeCatalog{}, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{}, &fakeAudit{},
	)
	_, svcErr := svc2.GetCrossAgentInsight(context.Background(), "missing")
	if svcErr == nil {
		t.Fatal("expected error for missing session")
	}
}
