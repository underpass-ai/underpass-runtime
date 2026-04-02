package grpcapi

import (
	"context"

	lpb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/learning/v1"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LearningEvidenceServer implements the P0 subset of the LearningEvidenceService.
type LearningEvidenceServer struct {
	lpb.UnimplementedLearningEvidenceServiceServer
	service LearningEvidenceService
}

// LearningEvidenceService is the interface the gRPC adapter calls for evidence queries.
type LearningEvidenceService interface {
	GetRecommendationDecision(ctx context.Context, recommendationID string) (domain.RecommendationDecision, *app.ServiceError)
	GetEvidenceBundle(ctx context.Context, recommendationID string) (app.EvidenceBundle, *app.ServiceError)
}

// NewLearningEvidenceServer creates the learning evidence gRPC server adapter.
func NewLearningEvidenceServer(svc LearningEvidenceService) *LearningEvidenceServer {
	return &LearningEvidenceServer{service: svc}
}

func (s *LearningEvidenceServer) GetRecommendationDecision(ctx context.Context, req *lpb.GetRecommendationDecisionRequest) (*lpb.GetRecommendationDecisionResponse, error) {
	d, svcErr := s.service.GetRecommendationDecision(ctx, req.GetRecommendationId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &lpb.GetRecommendationDecisionResponse{
		Decision: recommendationDecisionToProto(d),
	}, nil
}

func (s *LearningEvidenceServer) GetEvidenceBundle(ctx context.Context, req *lpb.GetEvidenceBundleRequest) (*lpb.GetEvidenceBundleResponse, error) {
	bundle, svcErr := s.service.GetEvidenceBundle(ctx, req.GetRecommendationId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &lpb.GetEvidenceBundleResponse{
		Bundle: &lpb.EvidenceBundle{
			Recommendation: recommendationDecisionToProto(bundle.Recommendation),
		},
	}, nil
}

func recommendationDecisionToProto(d domain.RecommendationDecision) *lpb.RecommendationDecision {
	items := make([]*lpb.RecommendationItem, len(d.Recommendations))
	for i, r := range d.Recommendations {
		items[i] = &lpb.RecommendationItem{
			ToolId:        r.ToolID,
			Rank:          int32(r.Rank),
			FinalScore:    r.FinalScore,
			Why:           r.Why,
			EstimatedCost: r.EstimatedCost,
		}
	}

	proto := &lpb.RecommendationDecision{
		RecommendationId: d.RecommendationID,
		SessionId:        d.SessionID,
		TenantId:         d.TenantID,
		ActorId:          d.ActorID,
		TaskHint:         d.TaskHint,
		TopK:             int32(d.TopK),
		ContextSignature: d.ContextSignature,
		AlgorithmId:      d.AlgorithmID,
		AlgorithmVersion: d.AlgorithmVersion,
		CandidateCount:   int32(d.CandidateCount),
		Recommendations:  items,
		CreatedAt:        timestamppb.New(d.CreatedAt),
		Event: &lpb.EventFact{
			EventId: d.EventID,
			Subject: d.EventSubject,
		},
	}

	// Map string decision_source to enum.
	switch d.DecisionSource {
	case app.DecisionSourceHeuristicOnly:
		proto.DecisionSource = lpb.DecisionSource_DECISION_SOURCE_HEURISTIC_ONLY
	case app.DecisionSourceHeuristicWithTelemetry:
		proto.DecisionSource = lpb.DecisionSource_DECISION_SOURCE_HEURISTIC_WITH_TELEMETRY
	case app.DecisionSourceHeuristicWithPolicy:
		proto.DecisionSource = lpb.DecisionSource_DECISION_SOURCE_HYBRID
	}

	// Map string policy_mode to enum.
	switch d.PolicyMode {
	case app.PolicyModeNone:
		proto.PolicyMode = lpb.PolicyMode_POLICY_MODE_NONE
	case app.PolicyModeShadow:
		proto.PolicyMode = lpb.PolicyMode_POLICY_MODE_SHADOW
	case "assist":
		proto.PolicyMode = lpb.PolicyMode_POLICY_MODE_ASSIST
	case "enforced":
		proto.PolicyMode = lpb.PolicyMode_POLICY_MODE_ENFORCED
	}

	return proto
}
