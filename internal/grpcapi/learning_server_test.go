package grpcapi

import (
	"context"
	"testing"
	"time"

	lpb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/learning/v1"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ─── Fake learning service ─────────────────────────────────────────────────

type fakeLearningService struct {
	decision domain.RecommendationDecision
	bundle   app.EvidenceBundle
	svcErr   *app.ServiceError
}

func (f *fakeLearningService) GetRecommendationDecision(_ context.Context, _ string) (domain.RecommendationDecision, *app.ServiceError) {
	return f.decision, f.svcErr
}

func (f *fakeLearningService) GetEvidenceBundle(_ context.Context, _ string) (app.EvidenceBundle, *app.ServiceError) {
	return f.bundle, f.svcErr
}

// ─── GetRecommendationDecision ─────────────────────────────────────────────

func TestLearningServer_GetRecommendationDecision(t *testing.T) {
	decision := domain.RecommendationDecision{
		RecommendationID: "rec-1",
		SessionID:        "sess-1",
		TenantID:         "t1",
		ActorID:          "a1",
		TaskHint:         "read file",
		TopK:             5,
		ContextSignature: "io:go:standard",
		DecisionSource:   app.DecisionSourceHeuristicOnly,
		AlgorithmID:      app.AlgorithmIDHeuristic,
		AlgorithmVersion: app.AlgorithmVersionV1,
		PolicyMode:       app.PolicyModeNone,
		CandidateCount:   10,
		EventID:          "evt-1",
		EventSubject:     "runtime.learning.recommendation.emitted",
		CreatedAt:        time.Now().UTC(),
		Recommendations: []domain.RankedToolEvidence{
			{ToolID: "fs.read_file", Rank: 1, FinalScore: 0.95, Why: "low risk", EstimatedCost: "cheap"},
		},
	}

	srv := NewLearningEvidenceServer(&fakeLearningService{decision: decision})
	resp, err := srv.GetRecommendationDecision(context.Background(),
		&lpb.GetRecommendationDecisionRequest{RecommendationId: "rec-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := resp.GetDecision()
	if d.GetRecommendationId() != "rec-1" {
		t.Fatalf("expected rec-1, got %s", d.GetRecommendationId())
	}
	if d.GetSessionId() != "sess-1" {
		t.Fatalf("expected sess-1, got %s", d.GetSessionId())
	}
	if d.GetDecisionSource() != lpb.DecisionSource_DECISION_SOURCE_HEURISTIC_ONLY {
		t.Fatalf("expected HEURISTIC_ONLY, got %v", d.GetDecisionSource())
	}
	if d.GetPolicyMode() != lpb.PolicyMode_POLICY_MODE_NONE {
		t.Fatalf("expected NONE, got %v", d.GetPolicyMode())
	}
	if len(d.GetRecommendations()) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(d.GetRecommendations()))
	}
	rec := d.GetRecommendations()[0]
	if rec.GetToolId() != "fs.read_file" {
		t.Fatalf("expected fs.read_file, got %s", rec.GetToolId())
	}
	if d.GetEvent().GetEventId() != "evt-1" {
		t.Fatalf("expected event evt-1, got %s", d.GetEvent().GetEventId())
	}
}

func TestLearningServer_GetRecommendationDecision_NotFound(t *testing.T) {
	srv := NewLearningEvidenceServer(&fakeLearningService{
		svcErr: &app.ServiceError{Code: "not_found", Message: "not found"},
	})
	_, err := srv.GetRecommendationDecision(context.Background(),
		&lpb.GetRecommendationDecisionRequest{RecommendationId: "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── GetEvidenceBundle ─────────────────────────────────────────────────────

func TestLearningServer_GetEvidenceBundle(t *testing.T) {
	bundle := app.EvidenceBundle{
		Recommendation: domain.RecommendationDecision{
			RecommendationID: "rec-2",
			DecisionSource:   app.DecisionSourceHeuristicWithTelemetry,
			PolicyMode:       app.PolicyModeShadow,
			AlgorithmID:      app.AlgorithmIDHeuristic,
		},
	}

	srv := NewLearningEvidenceServer(&fakeLearningService{bundle: bundle})
	resp, err := srv.GetEvidenceBundle(context.Background(),
		&lpb.GetEvidenceBundleRequest{RecommendationId: "rec-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := resp.GetBundle()
	if b.GetRecommendation().GetRecommendationId() != "rec-2" {
		t.Fatalf("expected rec-2, got %s", b.GetRecommendation().GetRecommendationId())
	}
	if b.GetRecommendation().GetDecisionSource() != lpb.DecisionSource_DECISION_SOURCE_HEURISTIC_WITH_TELEMETRY {
		t.Fatalf("expected HEURISTIC_WITH_TELEMETRY, got %v", b.GetRecommendation().GetDecisionSource())
	}
	if b.GetRecommendation().GetPolicyMode() != lpb.PolicyMode_POLICY_MODE_SHADOW {
		t.Fatalf("expected SHADOW, got %v", b.GetRecommendation().GetPolicyMode())
	}
}

func TestLearningServer_GetEvidenceBundle_NotFound(t *testing.T) {
	srv := NewLearningEvidenceServer(&fakeLearningService{
		svcErr: &app.ServiceError{Code: "not_found", Message: "not found"},
	})
	_, err := srv.GetEvidenceBundle(context.Background(),
		&lpb.GetEvidenceBundleRequest{RecommendationId: "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}
