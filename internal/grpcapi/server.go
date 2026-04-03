// Package grpcapi implements gRPC transport adapters for the workspace service.
package grpcapi

import (
	"context"
	"log/slog"

	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// WorkspaceService is the interface the gRPC adapter calls.
// Satisfied by *app.Service.
type WorkspaceService interface {
	CreateSession(ctx context.Context, req app.CreateSessionRequest) (domain.Session, *app.ServiceError)
	CloseSession(ctx context.Context, sessionID string) *app.ServiceError
	ListTools(ctx context.Context, sessionID string) ([]domain.Capability, *app.ServiceError)
	DiscoverTools(ctx context.Context, sessionID string, detail app.DiscoveryDetail, filter app.DiscoveryFilter) (app.DiscoveryResponse, *app.ServiceError)
	RecommendTools(ctx context.Context, sessionID string, taskHint string, topK int) (app.RecommendationsResponse, *app.ServiceError)
	GetRecommendationDecision(ctx context.Context, recommendationID string) (domain.RecommendationDecision, *app.ServiceError)
	GetEvidenceBundle(ctx context.Context, recommendationID string) (app.EvidenceBundle, *app.ServiceError)
	InvokeTool(ctx context.Context, sessionID, toolName string, req app.InvokeToolRequest) (domain.Invocation, *app.ServiceError)
	GetInvocation(ctx context.Context, invocationID string) (domain.Invocation, *app.ServiceError)
	GetInvocationLogs(ctx context.Context, invocationID string) ([]domain.LogLine, *app.ServiceError)
	GetInvocationArtifacts(ctx context.Context, invocationID string) ([]domain.Artifact, *app.ServiceError)
	ValidateSessionAccess(ctx context.Context, sessionID string, principal domain.Principal) *app.ServiceError
	ValidateInvocationAccess(ctx context.Context, invocationID string, principal domain.Principal) *app.ServiceError
	AcceptRecommendation(ctx context.Context, sessionID, recommendationID, selectedToolID string) (string, *app.ServiceError)
	RejectRecommendation(ctx context.Context, sessionID, recommendationID, reason string) (string, *app.ServiceError)
}

// Server implements the four gRPC services defined in runtime.proto.
type Server struct {
	pb.UnimplementedSessionServiceServer
	pb.UnimplementedCapabilityCatalogServiceServer
	pb.UnimplementedInvocationServiceServer
	pb.UnimplementedHealthServiceServer

	service WorkspaceService
	auth    AuthConfig
	logger  *slog.Logger
}

// NewServer creates the gRPC server adapter.
func NewServer(service WorkspaceService, auth AuthConfig, logger *slog.Logger) *Server {
	return &Server{service: service, auth: auth, logger: logger}
}

// ─── HealthService ──────────────────────────────────────────────────────────

func (s *Server) Check(_ context.Context, _ *pb.CheckRequest) (*pb.CheckResponse, error) {
	return &pb.CheckResponse{Status: "ok"}, nil
}

// ─── SessionService ─────────────────────────────────────────────────────────

func (s *Server) CreateSession(ctx context.Context, req *pb.CreateSessionRequest) (*pb.CreateSessionResponse, error) {
	appReq := protoToCreateSessionReq(req)

	if principal, ok := PrincipalFromContext(ctx); ok {
		appReq.Principal = principal
	}

	session, svcErr := s.service.CreateSession(ctx, appReq)
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.CreateSessionResponse{Session: sessionToProto(session)}, nil
}

func (s *Server) CloseSession(ctx context.Context, req *pb.CloseSessionRequest) (*pb.CloseSessionResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if svcErr := s.service.CloseSession(ctx, req.GetSessionId()); svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.CloseSessionResponse{Closed: true}, nil
}

// ─── CapabilityCatalogService ───────────────────────────────────────────────

func (s *Server) ListTools(ctx context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	tools, svcErr := s.service.ListTools(ctx, req.GetSessionId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	resp := &pb.ListToolsResponse{}
	for i := range tools {
		resp.Tools = append(resp.Tools, toolToProto(tools[i]))
	}
	return resp, nil
}

func (s *Server) DiscoverTools(ctx context.Context, req *pb.DiscoverToolsRequest) (*pb.DiscoverToolsResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	detail := protoToDiscoveryDetail(req.GetDetail())
	filter := protoToDiscoveryFilter(req)

	discovery, svcErr := s.service.DiscoverTools(ctx, req.GetSessionId(), detail, filter)
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}

	resp := &pb.DiscoverToolsResponse{
		Total:    int32(discovery.Total),
		Filtered: int32(discovery.Filtered),
	}

	switch tools := discovery.Tools.(type) {
	case []app.CompactTool:
		list := &pb.CompactToolList{}
		for i := range tools {
			list.Tools = append(list.Tools, compactToolToProto(tools[i]))
		}
		resp.Tools = &pb.DiscoverToolsResponse_Compact{Compact: list}
	case []app.FullTool:
		list := &pb.FullToolList{}
		for i := range tools {
			list.Tools = append(list.Tools, fullToolToProto(tools[i]))
		}
		resp.Tools = &pb.DiscoverToolsResponse_Full{Full: list}
	}
	return resp, nil
}

func (s *Server) RecommendTools(ctx context.Context, req *pb.RecommendToolsRequest) (*pb.RecommendToolsResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	result, svcErr := s.service.RecommendTools(ctx, req.GetSessionId(), req.GetTaskHint(), int(req.GetTopK()))
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	resp := &pb.RecommendToolsResponse{
		TaskHint:         result.TaskHint,
		TopK:             int32(result.TopK),
		RecommendationId: result.RecommendationID,
		EventId:          result.EventID,
		EventSubject:     result.EventSubject,
		DecisionSource:   result.DecisionSource,
		AlgorithmId:      result.AlgorithmID,
		AlgorithmVersion: result.AlgorithmVersion,
		PolicyMode:       result.PolicyMode,
	}
	for _, r := range result.Recommendations {
		resp.Recommendations = append(resp.Recommendations, recommendationToProto(r))
	}
	return resp, nil
}

func (s *Server) AcceptRecommendation(ctx context.Context, req *pb.AcceptRecommendationRequest) (*pb.AcceptRecommendationResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	eventID, svcErr := s.service.AcceptRecommendation(ctx, req.GetSessionId(), req.GetRecommendationId(), req.GetSelectedToolId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.AcceptRecommendationResponse{EventId: eventID}, nil
}

func (s *Server) RejectRecommendation(ctx context.Context, req *pb.RejectRecommendationRequest) (*pb.RejectRecommendationResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	eventID, svcErr := s.service.RejectRecommendation(ctx, req.GetSessionId(), req.GetRecommendationId(), req.GetReason())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.RejectRecommendationResponse{EventId: eventID}, nil
}

// ─── InvocationService ──────────────────────────────────────────────────────

func (s *Server) InvokeTool(ctx context.Context, req *pb.InvokeToolRequest) (*pb.InvokeToolResponse, error) {
	if err := s.validateSessionAuth(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	appReq := protoToInvokeToolReq(req)
	inv, svcErr := s.service.InvokeTool(ctx, req.GetSessionId(), req.GetToolName(), appReq)

	// Governed denials: return materialized Invocation, not gRPC error.
	if svcErr != nil && inv.ID != "" {
		return &pb.InvokeToolResponse{Invocation: invocationToProto(inv)}, nil
	}
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.InvokeToolResponse{Invocation: invocationToProto(inv)}, nil
}

func (s *Server) GetInvocation(ctx context.Context, req *pb.GetInvocationRequest) (*pb.GetInvocationResponse, error) {
	if err := s.validateInvocationAuth(ctx, req.GetInvocationId()); err != nil {
		return nil, err
	}
	inv, svcErr := s.service.GetInvocation(ctx, req.GetInvocationId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	return &pb.GetInvocationResponse{Invocation: invocationToProto(inv)}, nil
}

func (s *Server) GetInvocationLogs(ctx context.Context, req *pb.GetInvocationLogsRequest) (*pb.GetInvocationLogsResponse, error) {
	if err := s.validateInvocationAuth(ctx, req.GetInvocationId()); err != nil {
		return nil, err
	}
	logs, svcErr := s.service.GetInvocationLogs(ctx, req.GetInvocationId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	resp := &pb.GetInvocationLogsResponse{}
	for _, l := range logs {
		resp.Logs = append(resp.Logs, logLineToProto(l))
	}
	return resp, nil
}

func (s *Server) GetInvocationArtifacts(ctx context.Context, req *pb.GetInvocationArtifactsRequest) (*pb.GetInvocationArtifactsResponse, error) {
	if err := s.validateInvocationAuth(ctx, req.GetInvocationId()); err != nil {
		return nil, err
	}
	artifacts, svcErr := s.service.GetInvocationArtifacts(ctx, req.GetInvocationId())
	if svcErr != nil {
		return nil, serviceErrorToStatus(svcErr)
	}
	resp := &pb.GetInvocationArtifactsResponse{}
	for _, a := range artifacts {
		resp.Artifacts = append(resp.Artifacts, artifactToProto(a))
	}
	return resp, nil
}

// ─── Auth helpers ───────────────────────────────────────────────────────────

func (s *Server) validateSessionAuth(ctx context.Context, sessionID string) error {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil // auth not enabled
	}
	if svcErr := s.service.ValidateSessionAccess(ctx, sessionID, principal); svcErr != nil {
		return serviceErrorToStatus(svcErr)
	}
	return nil
}

func (s *Server) validateInvocationAuth(ctx context.Context, invocationID string) error {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil
	}
	if svcErr := s.service.ValidateInvocationAccess(ctx, invocationID, principal); svcErr != nil {
		return serviceErrorToStatus(svcErr)
	}
	return nil
}
