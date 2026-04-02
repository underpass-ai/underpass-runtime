package grpcapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// ─── Fake service ───────────────────────────────────────────────────────────

type fakeService struct {
	session     domain.Session
	tools       []domain.Capability
	invocation  domain.Invocation
	logs        []domain.LogLine
	artifacts   []domain.Artifact
	discovery   app.DiscoveryResponse
	recommend   app.RecommendationsResponse
	svcErr      *app.ServiceError
	invokeErr   *app.ServiceError
	accessErr   *app.ServiceError
	invocations int
}

func (f *fakeService) CreateSession(_ context.Context, _ app.CreateSessionRequest) (domain.Session, *app.ServiceError) {
	return f.session, f.svcErr
}
func (f *fakeService) CloseSession(_ context.Context, _ string) *app.ServiceError {
	return f.svcErr
}
func (f *fakeService) ListTools(_ context.Context, _ string) ([]domain.Capability, *app.ServiceError) {
	return f.tools, f.svcErr
}
func (f *fakeService) DiscoverTools(_ context.Context, _ string, _ app.DiscoveryDetail, _ app.DiscoveryFilter) (app.DiscoveryResponse, *app.ServiceError) {
	return f.discovery, f.svcErr
}
func (f *fakeService) RecommendTools(_ context.Context, _ string, _ string, _ int) (app.RecommendationsResponse, *app.ServiceError) {
	return f.recommend, f.svcErr
}
func (f *fakeService) InvokeTool(_ context.Context, _ string, _ string, _ app.InvokeToolRequest) (domain.Invocation, *app.ServiceError) {
	f.invocations++
	return f.invocation, f.invokeErr
}
func (f *fakeService) GetInvocation(_ context.Context, _ string) (domain.Invocation, *app.ServiceError) {
	return f.invocation, f.svcErr
}
func (f *fakeService) GetInvocationLogs(_ context.Context, _ string) ([]domain.LogLine, *app.ServiceError) {
	return f.logs, f.svcErr
}
func (f *fakeService) GetInvocationArtifacts(_ context.Context, _ string) ([]domain.Artifact, *app.ServiceError) {
	return f.artifacts, f.svcErr
}
func (f *fakeService) ValidateSessionAccess(_ context.Context, _ string, _ domain.Principal) *app.ServiceError {
	return f.accessErr
}
func (f *fakeService) ValidateInvocationAccess(_ context.Context, _ string, _ domain.Principal) *app.ServiceError {
	return f.accessErr
}
func (f *fakeService) GetRecommendationDecision(_ context.Context, _ string) (domain.RecommendationDecision, *app.ServiceError) {
	return domain.RecommendationDecision{}, f.svcErr
}
func (f *fakeService) GetEvidenceBundle(_ context.Context, _ string) (app.EvidenceBundle, *app.ServiceError) {
	return app.EvidenceBundle{}, f.svcErr
}
func (f *fakeService) PrometheusMetrics() string { return "" }

func newTestServer(fake *fakeService) *Server {
	return &Server{
		service: fake,
		auth:    DefaultAuthConfig(),
		logger:  slog.Default(),
	}
}

// ─── Health ─────────────────────────────────────────────────────────────────

func TestHealth_Check(t *testing.T) {
	srv := newTestServer(&fakeService{})
	resp, err := srv.Check(context.Background(), &pb.CheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != "ok" {
		t.Errorf("status = %q, want ok", resp.GetStatus())
	}
}

// ─── Session ────────────────────────────────────────────────────────────────

func TestCreateSession(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeService{
		session: domain.Session{
			ID:            "sess-1",
			WorkspacePath: "/tmp/ws",
			Principal:     domain.Principal{TenantID: "t1", ActorID: "a1", Roles: []string{"dev"}},
			CreatedAt:     now,
			ExpiresAt:     now.Add(time.Hour),
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.CreateSession(context.Background(), &pb.CreateSessionRequest{
		Principal: &pb.Principal{TenantId: "t1", ActorId: "a1", Roles: []string{"dev"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSession().GetId() != "sess-1" {
		t.Errorf("session.id = %q, want sess-1", resp.GetSession().GetId())
	}
}

func TestCreateSession_Error(t *testing.T) {
	fake := &fakeService{
		svcErr: &app.ServiceError{Code: app.ErrorCodeInvalidArgument, Message: "bad", HTTPStatus: 400},
	}
	srv := newTestServer(fake)

	_, err := srv.CreateSession(context.Background(), &pb.CreateSessionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", s.Code())
	}
}

func TestCloseSession(t *testing.T) {
	srv := newTestServer(&fakeService{})
	resp, err := srv.CloseSession(context.Background(), &pb.CloseSessionRequest{SessionId: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetClosed() {
		t.Error("expected closed=true")
	}
}

// ─── Catalog ────────────────────────────────────────────────────────────────

func TestListTools(t *testing.T) {
	fake := &fakeService{
		tools: []domain.Capability{
			{Name: "fs.read_file", Description: "read", RiskLevel: domain.RiskLow, Scope: domain.ScopeWorkspace},
			{Name: "git.commit", Description: "commit", RiskLevel: domain.RiskMedium, Scope: domain.ScopeRepo},
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.ListTools(context.Background(), &pb.ListToolsRequest{SessionId: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTools()) != 2 {
		t.Fatalf("tools = %d, want 2", len(resp.GetTools()))
	}
	if resp.GetTools()[0].GetName() != "fs.read_file" {
		t.Errorf("tools[0].name = %q", resp.GetTools()[0].GetName())
	}
}

func TestDiscoverTools_Compact(t *testing.T) {
	fake := &fakeService{
		discovery: app.DiscoveryResponse{
			Tools: []app.CompactTool{
				{Name: "fs.list", Description: "list files", Risk: "low"},
			},
			Total:    99,
			Filtered: 1,
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.DiscoverTools(context.Background(), &pb.DiscoverToolsRequest{
		SessionId: "s1",
		Detail:    pb.DiscoveryDetail_DISCOVERY_DETAIL_COMPACT,
	})
	if err != nil {
		t.Fatal(err)
	}
	compact := resp.GetCompact()
	if compact == nil {
		t.Fatal("expected compact tools")
	}
	if len(compact.GetTools()) != 1 {
		t.Fatalf("compact tools = %d, want 1", len(compact.GetTools()))
	}
	if resp.GetTotal() != 99 {
		t.Errorf("total = %d, want 99", resp.GetTotal())
	}
}

func TestDiscoverTools_Full(t *testing.T) {
	fake := &fakeService{
		discovery: app.DiscoveryResponse{
			Tools: []app.FullTool{
				{Capability: domain.Capability{Name: "fs.write_file"}, Tags: []string{"fs"}, Cost: "low"},
			},
			Total:    99,
			Filtered: 1,
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.DiscoverTools(context.Background(), &pb.DiscoverToolsRequest{
		SessionId: "s1",
		Detail:    pb.DiscoveryDetail_DISCOVERY_DETAIL_FULL,
	})
	if err != nil {
		t.Fatal(err)
	}
	full := resp.GetFull()
	if full == nil {
		t.Fatal("expected full tools")
	}
	if len(full.GetTools()) != 1 {
		t.Fatalf("full tools = %d, want 1", len(full.GetTools()))
	}
}

func TestRecommendTools(t *testing.T) {
	fake := &fakeService{
		recommend: app.RecommendationsResponse{
			Recommendations: []app.Recommendation{
				{Name: "fs.write_file", Score: 0.95, Why: "high success rate"},
			},
			TaskHint: "create a file",
			TopK:     5,
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.RecommendTools(context.Background(), &pb.RecommendToolsRequest{
		SessionId: "s1", TaskHint: "create a file", TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetRecommendations()) != 1 {
		t.Fatalf("recommendations = %d, want 1", len(resp.GetRecommendations()))
	}
	if resp.GetRecommendations()[0].GetScore() != 0.95 {
		t.Errorf("score = %f, want 0.95", resp.GetRecommendations()[0].GetScore())
	}
}

// ─── Invocation ─────────────────────────────────────────────────────────────

func TestInvokeTool(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeService{
		invocation: domain.Invocation{
			ID:        "inv-1",
			SessionID: "s1",
			ToolName:  "fs.write_file",
			Status:    domain.InvocationStatusSucceeded,
			StartedAt: now,
			Output:    map[string]any{"bytes_written": 42},
		},
	}
	srv := newTestServer(fake)

	args, _ := structpb.NewStruct(map[string]any{"path": "test.go", "content": "package main"})
	resp, err := srv.InvokeTool(context.Background(), &pb.InvokeToolRequest{
		SessionId: "s1", ToolName: "fs.write_file", Args: args, Approved: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetInvocation().GetId() != "inv-1" {
		t.Errorf("invocation.id = %q", resp.GetInvocation().GetId())
	}
	if resp.GetInvocation().GetStatus() != pb.InvocationStatus_INVOCATION_STATUS_SUCCEEDED {
		t.Errorf("status = %v", resp.GetInvocation().GetStatus())
	}
}

func TestInvokeTool_GovernedDenial(t *testing.T) {
	fake := &fakeService{
		invocation: domain.Invocation{
			ID:       "inv-denied",
			Status:   domain.InvocationStatusDenied,
			ToolName: "k8s.apply",
			Error:    &domain.Error{Code: "policy_denied", Message: "not authorized"},
		},
		invokeErr: &app.ServiceError{Code: app.ErrorCodePolicyDenied, Message: "not authorized", HTTPStatus: 403},
	}
	srv := newTestServer(fake)

	resp, err := srv.InvokeTool(context.Background(), &pb.InvokeToolRequest{
		SessionId: "s1", ToolName: "k8s.apply",
	})
	// Governed denial returns invocation, not gRPC error.
	if err != nil {
		t.Fatalf("governed denial should not return error, got: %v", err)
	}
	if resp.GetInvocation().GetStatus() != pb.InvocationStatus_INVOCATION_STATUS_DENIED {
		t.Errorf("status = %v, want DENIED", resp.GetInvocation().GetStatus())
	}
	if resp.GetInvocation().GetError().GetCode() != "policy_denied" {
		t.Errorf("error.code = %q", resp.GetInvocation().GetError().GetCode())
	}
}

func TestGetInvocation(t *testing.T) {
	fake := &fakeService{
		invocation: domain.Invocation{ID: "inv-1", ToolName: "fs.list"},
	}
	srv := newTestServer(fake)

	resp, err := srv.GetInvocation(context.Background(), &pb.GetInvocationRequest{InvocationId: "inv-1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetInvocation().GetToolName() != "fs.list" {
		t.Errorf("tool_name = %q", resp.GetInvocation().GetToolName())
	}
}

func TestGetInvocationLogs(t *testing.T) {
	fake := &fakeService{
		logs: []domain.LogLine{
			{At: time.Now(), Channel: "stdout", Message: "hello"},
			{At: time.Now(), Channel: "stderr", Message: "warn"},
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.GetInvocationLogs(context.Background(), &pb.GetInvocationLogsRequest{InvocationId: "inv-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetLogs()) != 2 {
		t.Fatalf("logs = %d, want 2", len(resp.GetLogs()))
	}
}

func TestGetInvocationArtifacts(t *testing.T) {
	fake := &fakeService{
		artifacts: []domain.Artifact{
			{ID: "art-1", Name: "output.json", SizeBytes: 256},
		},
	}
	srv := newTestServer(fake)

	resp, err := srv.GetInvocationArtifacts(context.Background(), &pb.GetInvocationArtifactsRequest{InvocationId: "inv-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetArtifacts()) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(resp.GetArtifacts()))
	}
}

func TestGetInvocation_NotFound(t *testing.T) {
	fake := &fakeService{
		svcErr: &app.ServiceError{Code: app.ErrorCodeNotFound, Message: "not found", HTTPStatus: 404},
	}
	srv := newTestServer(fake)

	_, err := srv.GetInvocation(context.Background(), &pb.GetInvocationRequest{InvocationId: "nope"})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", s.Code())
	}
}

// ─── Auth ───────────────────────────────────────────────────────────────────

func TestAuth_Interceptor_Disabled(t *testing.T) {
	cfg := DefaultAuthConfig() // payload mode = no auth
	interceptor := UnaryAuthInterceptor(cfg)

	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		_, ok := PrincipalFromContext(ctx)
		if ok {
			t.Error("principal should not be in context when auth disabled")
		}
		return nil, nil
	}

	_, err := interceptor(context.Background(), nil, nil, handler)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("handler not called")
	}
}

func TestAuth_Interceptor_ValidToken(t *testing.T) {
	cfg := AuthConfig{
		Mode:        authModeTrustedHeaders,
		TokenKey:    "x-workspace-auth-token",
		TenantKey:   "x-workspace-tenant-id",
		ActorKey:    "x-workspace-actor-id",
		RolesKey:    "x-workspace-roles",
		SharedToken: "secret123",
	}
	interceptor := UnaryAuthInterceptor(cfg)

	md := metadata.Pairs(
		"x-workspace-auth-token", "secret123",
		"x-workspace-tenant-id", "acme",
		"x-workspace-actor-id", "agent-1",
		"x-workspace-roles", "developer,devops",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, req any) (any, error) {
		p, ok := PrincipalFromContext(ctx)
		if !ok {
			t.Fatal("expected principal in context")
		}
		if p.TenantID != "acme" || p.ActorID != "agent-1" {
			t.Errorf("principal = %+v", p)
		}
		if len(p.Roles) != 2 {
			t.Errorf("roles = %v", p.Roles)
		}
		return nil, nil
	}

	_, err := interceptor(ctx, nil, nil, handler)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuth_Interceptor_BadToken(t *testing.T) {
	cfg := AuthConfig{
		Mode:        authModeTrustedHeaders,
		TokenKey:    "x-workspace-auth-token",
		TenantKey:   "x-workspace-tenant-id",
		ActorKey:    "x-workspace-actor-id",
		SharedToken: "secret123",
	}
	interceptor := UnaryAuthInterceptor(cfg)

	md := metadata.Pairs("x-workspace-auth-token", "wrong")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(ctx, nil, nil, func(context.Context, any) (any, error) {
		t.Fatal("handler should not be called")
		return nil, nil
	})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", s.Code())
	}
}

func TestAuth_Interceptor_MissingMetadata(t *testing.T) {
	cfg := AuthConfig{
		Mode:        authModeTrustedHeaders,
		TokenKey:    "x-workspace-auth-token",
		SharedToken: "secret123",
	}
	interceptor := UnaryAuthInterceptor(cfg)

	// No metadata at all.
	_, err := interceptor(context.Background(), nil, nil, func(context.Context, any) (any, error) {
		return nil, nil
	})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", s.Code())
	}
}

func TestParseRoles_Dedup(t *testing.T) {
	roles := parseRoles("dev, dev, ops, , ops")
	if len(roles) != 2 {
		t.Errorf("roles = %v, want [dev ops]", roles)
	}
}

// ─── Converters ─────────────────────────────────────────────────────────────

func TestAnyToProtoValue_Object(t *testing.T) {
	v := anyToProtoValue(map[string]any{"key": "val"})
	if v.GetStructValue() == nil {
		t.Error("expected struct value")
	}
}

func TestAnyToProtoValue_String(t *testing.T) {
	v := anyToProtoValue("hello")
	if v.GetStringValue() != "hello" {
		t.Errorf("got %v", v)
	}
}

func TestAnyToProtoValue_Nil(t *testing.T) {
	v := anyToProtoValue(nil)
	if v.GetNullValue() != 0 {
		t.Error("expected null value")
	}
}

func TestAnyToProtoValue_Number(t *testing.T) {
	v := anyToProtoValue(42.5)
	if v.GetNumberValue() != 42.5 {
		t.Errorf("got %v", v.GetNumberValue())
	}
}

func TestServiceErrorToStatus_Mapping(t *testing.T) {
	tests := []struct {
		code string
		want codes.Code
	}{
		{app.ErrorCodeInvalidArgument, codes.InvalidArgument},
		{app.ErrorCodeNotFound, codes.NotFound},
		{app.ErrorCodePolicyDenied, codes.PermissionDenied},
		{app.ErrorCodeApprovalRequired, codes.FailedPrecondition},
		{app.ErrorCodeTimeout, codes.DeadlineExceeded},
		{app.ErrorCodeInternal, codes.Internal},
		{app.ErrorCodeExecutionFailed, codes.Internal},
	}
	for _, tt := range tests {
		err := serviceErrorToStatus(&app.ServiceError{Code: tt.code, Message: "test"})
		s, _ := status.FromError(err)
		if s.Code() != tt.want {
			t.Errorf("code %q → %v, want %v", tt.code, s.Code(), tt.want)
		}
	}
}

func TestServiceErrorToStatus_Nil(t *testing.T) {
	if serviceErrorToStatus(nil) != nil {
		t.Error("nil error should return nil")
	}
}

func TestProtoToInvokeToolReq(t *testing.T) {
	args, _ := structpb.NewStruct(map[string]any{"path": "main.go"})
	req := protoToInvokeToolReq(&pb.InvokeToolRequest{
		CorrelationId: "corr-1",
		Args:          args,
		Approved:      true,
	})
	if req.CorrelationID != "corr-1" || !req.Approved {
		t.Errorf("req = %+v", req)
	}
	var parsed map[string]any
	json.Unmarshal(req.Args, &parsed)
	if parsed["path"] != "main.go" {
		t.Errorf("args.path = %v", parsed["path"])
	}
}

func TestToolToProto_AllEnums(t *testing.T) {
	cap := domain.Capability{
		Name:             "k8s.apply",
		Description:      "apply manifest",
		Scope:            domain.ScopeCluster,
		SideEffects:      domain.SideEffectsIrreversible,
		RiskLevel:        domain.RiskHigh,
		RequiresApproval: true,
		Idempotency:      domain.IdempotencyNone,
		CostHint:         "high",
		Preconditions:    []string{"namespace must exist"},
		Postconditions:   []string{"resource created"},
		Observability:    domain.Observability{TraceName: "k8s", SpanName: "apply"},
	}
	tool := toolToProto(cap)
	if tool.GetScope() != pb.Scope_SCOPE_CLUSTER {
		t.Errorf("scope = %v", tool.GetScope())
	}
	if tool.GetSideEffects() != pb.SideEffects_SIDE_EFFECTS_IRREVERSIBLE {
		t.Errorf("side_effects = %v", tool.GetSideEffects())
	}
	if tool.GetRiskLevel() != pb.RiskLevel_RISK_LEVEL_HIGH {
		t.Errorf("risk = %v", tool.GetRiskLevel())
	}
	if tool.GetIdempotency() != pb.Idempotency_IDEMPOTENCY_NONE {
		t.Errorf("idempotency = %v", tool.GetIdempotency())
	}
}

func TestInvocationToProto_WithCompletedAt(t *testing.T) {
	now := time.Now().UTC()
	completed := now.Add(time.Second)
	inv := domain.Invocation{
		ID:          "inv-full",
		Status:      domain.InvocationStatusFailed,
		StartedAt:   now,
		CompletedAt: &completed,
		ExitCode:    1,
		Output:      []any{1, 2, 3},
		Logs:        []domain.LogLine{{At: now, Channel: "stderr", Message: "err"}},
		Artifacts:   []domain.Artifact{{ID: "a1", Name: "out.json"}},
		Error:       &domain.Error{Code: "timeout", Message: "timed out", Retryable: true},
	}
	_ = invocationToProto(inv)
}

func TestRuntimeKindToProto(t *testing.T) {
	tests := []struct {
		in   domain.RuntimeKind
		want pb.RuntimeKind
	}{
		{domain.RuntimeKindLocal, pb.RuntimeKind_RUNTIME_KIND_LOCAL},
		{domain.RuntimeKindDocker, pb.RuntimeKind_RUNTIME_KIND_DOCKER},
		{domain.RuntimeKindKubernetes, pb.RuntimeKind_RUNTIME_KIND_KUBERNETES},
		{"unknown", pb.RuntimeKind_RUNTIME_KIND_UNSPECIFIED},
	}
	for _, tt := range tests {
		got := runtimeKindToProto(tt.in)
		if got != tt.want {
			t.Errorf("runtimeKindToProto(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestEnumConverters_AllValues(t *testing.T) {
	// Scope
	for _, s := range []domain.Scope{domain.ScopeRepo, domain.ScopeWorkspace, domain.ScopeCluster, domain.ScopeExternal, "x"} {
		_ = scopeToProto(s)
	}
	// SideEffects
	for _, s := range []domain.SideEffects{domain.SideEffectsNone, domain.SideEffectsReversible, domain.SideEffectsIrreversible, "x"} {
		_ = sideEffectsToProto(s)
	}
	// RiskLevel
	for _, r := range []domain.RiskLevel{domain.RiskLow, domain.RiskMedium, domain.RiskHigh, "x"} {
		_ = riskLevelToProto(r)
	}
	// Idempotency
	for _, i := range []domain.Idempotency{domain.IdempotencyGuaranteed, domain.IdempotencyBestEffort, domain.IdempotencyNone, "x"} {
		_ = idempotencyToProto(i)
	}
	// InvocationStatus
	for _, s := range []domain.InvocationStatus{domain.InvocationStatusRunning, domain.InvocationStatusSucceeded, domain.InvocationStatusFailed, domain.InvocationStatusDenied, "x"} {
		_ = invocationStatusToProto(s)
	}
}

func TestFullToolToProto_WithStats(t *testing.T) {
	ft := app.FullTool{
		Capability: domain.Capability{Name: "fs.read_file", Idempotency: domain.IdempotencyGuaranteed},
		Tags:       []string{"fs"},
		Cost:       "low",
		Stats:      &app.ToolStats{SuccessRate: 0.99, P50Duration: 10, P95Duration: 50, InvocationN: 1000},
	}
	proto := fullToolToProto(ft)
	if proto.GetStats() == nil {
		t.Fatal("expected stats")
	}
	if proto.GetStats().GetSuccessRate() != 0.99 {
		t.Errorf("success_rate = %f", proto.GetStats().GetSuccessRate())
	}
}

func TestPolicyMetadataToProto_AllFields(t *testing.T) {
	pm := domain.PolicyMetadata{
		PathFields:      []domain.PolicyPathField{{Field: "path", Multi: false, WorkspaceRelative: true}},
		ArgFields:       []domain.PolicyArgField{{Field: "cmd", MaxLength: 100, DenyCharacters: []string{";", "|"}}},
		ProfileFields:   []domain.PolicyProfileField{{Field: "profile"}},
		SubjectFields:   []domain.PolicySubjectField{{Field: "subject"}},
		TopicFields:     []domain.PolicyTopicField{{Field: "topic"}},
		QueueFields:     []domain.PolicyQueueField{{Field: "queue"}},
		KeyPrefixFields: []domain.PolicyKeyPrefixField{{Field: "prefix"}},
		NamespaceFields: []string{"default"},
		RegistryFields:  []string{"ghcr.io"},
	}
	proto := policyMetadataToProto(pm)
	if len(proto.GetPathFields()) != 1 || len(proto.GetArgFields()) != 1 {
		t.Errorf("path=%d arg=%d", len(proto.GetPathFields()), len(proto.GetArgFields()))
	}
	if len(proto.GetProfileFields()) != 1 || len(proto.GetNamespaceFields()) != 1 {
		t.Errorf("profile=%d ns=%d", len(proto.GetProfileFields()), len(proto.GetNamespaceFields()))
	}
}

func TestAuthConfigFromEnv_Default(t *testing.T) {
	for _, k := range []string{"WORKSPACE_AUTH_MODE", "WORKSPACE_AUTH_SHARED_TOKEN"} {
		t.Setenv(k, "")
	}
	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != authModePayload {
		t.Errorf("mode = %q, want payload", cfg.Mode)
	}
}

func TestAuthConfigFromEnv_TrustedHeaders(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", "trusted_headers")
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "tok")
	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AuthEnabled() {
		t.Error("expected auth enabled")
	}
}

func TestCloseSession_AccessDenied(t *testing.T) {
	fake := &fakeService{
		accessErr: &app.ServiceError{Code: app.ErrorCodePolicyDenied, Message: "denied", HTTPStatus: 403},
	}
	srv := &Server{service: fake, auth: AuthConfig{Mode: authModeTrustedHeaders, SharedToken: "s", TokenKey: "x-workspace-auth-token", TenantKey: "x-workspace-tenant-id", ActorKey: "x-workspace-actor-id"}, logger: slog.Default()}

	md := metadata.Pairs("x-workspace-auth-token", "s", "x-workspace-tenant-id", "t", "x-workspace-actor-id", "a")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ctx = context.WithValue(ctx, principalKey{}, domain.Principal{TenantID: "t", ActorID: "a"})

	_, err := srv.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: "s1"})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", s.Code())
	}
}

func TestGetInvocation_AccessDenied(t *testing.T) {
	fake := &fakeService{
		accessErr: &app.ServiceError{Code: app.ErrorCodePolicyDenied, Message: "denied", HTTPStatus: 403},
	}
	srv := &Server{service: fake, auth: DefaultAuthConfig(), logger: slog.Default()}

	// With principal in context (simulates auth enabled).
	ctx := context.WithValue(context.Background(), principalKey{}, domain.Principal{TenantID: "t", ActorID: "a"})
	_, err := srv.GetInvocation(ctx, &pb.GetInvocationRequest{InvocationId: "inv-1"})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", s.Code())
	}
}

func TestInvokeTool_TransportError(t *testing.T) {
	fake := &fakeService{
		invocation: domain.Invocation{}, // empty ID = not materialized
		invokeErr:  &app.ServiceError{Code: app.ErrorCodeInvalidArgument, Message: "bad args", HTTPStatus: 400},
	}
	srv := newTestServer(fake)

	_, err := srv.InvokeTool(context.Background(), &pb.InvokeToolRequest{SessionId: "s1", ToolName: "bad"})
	if s, ok := status.FromError(err); !ok || s.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", s.Code())
	}
}

func TestAuthConfigFromEnv_TrustedHeaders_MissingToken(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", "trusted_headers")
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "")
	_, err := AuthConfigFromEnv()
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}
