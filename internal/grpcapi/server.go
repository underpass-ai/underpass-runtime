// Package grpcapi implements gRPC transport adapters for the workspace service.
package grpcapi

import (
	"context"
	"log/slog"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
)

// Server implements the four gRPC services defined in runtime.proto.
type Server struct {
	pb.UnimplementedSessionServiceServer
	pb.UnimplementedCapabilityCatalogServiceServer
	pb.UnimplementedInvocationServiceServer
	pb.UnimplementedHealthServiceServer

	service *app.Service
	auth    AuthConfig
	logger  *slog.Logger
}

// NewServer creates the gRPC server adapter.
func NewServer(service *app.Service, auth AuthConfig, logger *slog.Logger) *Server {
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
	for _, t := range tools {
		resp.Tools = append(resp.Tools, toolToProto(t))
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
		for _, t := range tools {
			list.Tools = append(list.Tools, compactToolToProto(t))
		}
		resp.Tools = &pb.DiscoverToolsResponse_Compact{Compact: list}
	case []app.FullTool:
		list := &pb.FullToolList{}
		for _, t := range tools {
			list.Tools = append(list.Tools, fullToolToProto(t))
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
		TaskHint: result.TaskHint,
		TopK:     int32(result.TopK),
	}
	for _, r := range result.Recommendations {
		resp.Recommendations = append(resp.Recommendations, recommendationToProto(r))
	}
	return resp, nil
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
