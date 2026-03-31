package grpcapi

import (
	"encoding/json"

	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── Proto → Domain ─────────────────────────────────────────────────────────

func protoToCreateSessionReq(r *pb.CreateSessionRequest) app.CreateSessionRequest {
	req := app.CreateSessionRequest{
		SessionID:       r.GetSessionId(),
		RepoURL:         r.GetRepoUrl(),
		RepoRef:         r.GetRepoRef(),
		SourceRepoPath:  r.GetSourceRepoPath(),
		AllowedPaths:    r.GetAllowedPaths(),
		Metadata:        r.GetMetadata(),
		ExpiresInSecond: int(r.GetExpiresInSeconds()),
	}
	if p := r.GetPrincipal(); p != nil {
		req.Principal = domain.Principal{
			TenantID: p.GetTenantId(),
			ActorID:  p.GetActorId(),
			Roles:    p.GetRoles(),
		}
	}
	return req
}

func protoToInvokeToolReq(r *pb.InvokeToolRequest) app.InvokeToolRequest {
	var args json.RawMessage
	if r.GetArgs() != nil {
		args, _ = r.GetArgs().MarshalJSON()
	}
	return app.InvokeToolRequest{
		CorrelationID: r.GetCorrelationId(),
		Args:          args,
		Approved:      r.GetApproved(),
	}
}

func protoToDiscoveryDetail(d pb.DiscoveryDetail) app.DiscoveryDetail {
	switch d {
	case pb.DiscoveryDetail_DISCOVERY_DETAIL_FULL:
		return app.DiscoveryDetailFull
	default:
		return app.DiscoveryDetailCompact
	}
}

func protoToDiscoveryFilter(r *pb.DiscoverToolsRequest) app.DiscoveryFilter {
	return app.DiscoveryFilter{
		Risk:        r.GetRisk(),
		Tags:        r.GetTags(),
		SideEffects: r.GetSideEffects(),
		Scope:       r.GetScope(),
		Cost:        r.GetCost(),
	}
}

// ─── Domain → Proto ─────────────────────────────────────────────────────────

func sessionToProto(s domain.Session) *pb.Session {
	return &pb.Session{
		Id:            s.ID,
		WorkspacePath: s.WorkspacePath,
		Runtime:       runtimeRefToProto(s.Runtime),
		RepoUrl:       s.RepoURL,
		RepoRef:       s.RepoRef,
		AllowedPaths:  s.AllowedPaths,
		Principal:     principalToProto(s.Principal),
		Metadata:      s.Metadata,
		CreatedAt:     timestamppb.New(s.CreatedAt),
		ExpiresAt:     timestamppb.New(s.ExpiresAt),
	}
}

func principalToProto(p domain.Principal) *pb.Principal {
	return &pb.Principal{
		TenantId: p.TenantID,
		ActorId:  p.ActorID,
		Roles:    p.Roles,
	}
}

func runtimeRefToProto(r domain.RuntimeRef) *pb.RuntimeRef {
	return &pb.RuntimeRef{
		Kind:        runtimeKindToProto(r.Kind),
		Namespace:   r.Namespace,
		PodName:     r.PodName,
		Container:   r.Container,
		ContainerId: r.ContainerID,
		Workdir:     r.Workdir,
	}
}

func runtimeKindToProto(k domain.RuntimeKind) pb.RuntimeKind {
	switch k {
	case domain.RuntimeKindLocal:
		return pb.RuntimeKind_RUNTIME_KIND_LOCAL
	case domain.RuntimeKindDocker:
		return pb.RuntimeKind_RUNTIME_KIND_DOCKER
	case domain.RuntimeKindKubernetes:
		return pb.RuntimeKind_RUNTIME_KIND_KUBERNETES
	default:
		return pb.RuntimeKind_RUNTIME_KIND_UNSPECIFIED
	}
}

func invocationToProto(inv domain.Invocation) *pb.Invocation {
	p := &pb.Invocation{
		Id:            inv.ID,
		SessionId:     inv.SessionID,
		ToolName:      inv.ToolName,
		CorrelationId: inv.CorrelationID,
		Status:        invocationStatusToProto(inv.Status),
		StartedAt:     timestamppb.New(inv.StartedAt),
		DurationMs:    inv.DurationMS,
		TraceName:     inv.TraceName,
		SpanName:      inv.SpanName,
		ExitCode:      int32(inv.ExitCode),
		OutputRef:     inv.OutputRef,
		LogsRef:       inv.LogsRef,
	}
	if inv.CompletedAt != nil {
		p.CompletedAt = timestamppb.New(*inv.CompletedAt)
	}
	if inv.Output != nil {
		p.Output = anyToProtoValue(inv.Output)
	}
	for _, l := range inv.Logs {
		p.Logs = append(p.Logs, logLineToProto(l))
	}
	for _, a := range inv.Artifacts {
		p.Artifacts = append(p.Artifacts, artifactToProto(a))
	}
	if inv.Error != nil {
		p.Error = &pb.Error{
			Code:      inv.Error.Code,
			Message:   inv.Error.Message,
			Retryable: inv.Error.Retryable,
		}
	}
	return p
}

func invocationStatusToProto(s domain.InvocationStatus) pb.InvocationStatus {
	switch s {
	case domain.InvocationStatusRunning:
		return pb.InvocationStatus_INVOCATION_STATUS_RUNNING
	case domain.InvocationStatusSucceeded:
		return pb.InvocationStatus_INVOCATION_STATUS_SUCCEEDED
	case domain.InvocationStatusFailed:
		return pb.InvocationStatus_INVOCATION_STATUS_FAILED
	case domain.InvocationStatusDenied:
		return pb.InvocationStatus_INVOCATION_STATUS_DENIED
	default:
		return pb.InvocationStatus_INVOCATION_STATUS_UNSPECIFIED
	}
}

func logLineToProto(l domain.LogLine) *pb.LogLine {
	return &pb.LogLine{
		At:      timestamppb.New(l.At),
		Channel: l.Channel,
		Message: l.Message,
	}
}

func artifactToProto(a domain.Artifact) *pb.Artifact {
	return &pb.Artifact{
		Id:          a.ID,
		Name:        a.Name,
		Path:        a.Path,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		Sha256:      a.SHA256,
		CreatedAt:   timestamppb.New(a.CreatedAt),
	}
}

func toolToProto(c domain.Capability) *pb.Tool {
	t := &pb.Tool{
		Name:             c.Name,
		Description:      c.Description,
		InputSchema:      c.InputSchema,
		OutputSchema:     c.OutputSchema,
		Scope:            scopeToProto(c.Scope),
		SideEffects:      sideEffectsToProto(c.SideEffects),
		RiskLevel:        riskLevelToProto(c.RiskLevel),
		RequiresApproval: c.RequiresApproval,
		Idempotency:      idempotencyToProto(c.Idempotency),
		CostHint:         c.CostHint,
		Preconditions:    c.Preconditions,
		Postconditions:   c.Postconditions,
		Constraints: &pb.Constraints{
			TimeoutSeconds: int32(c.Constraints.TimeoutSeconds),
			MaxRetries:     int32(c.Constraints.MaxRetries),
			AllowedPaths:   c.Constraints.AllowedPaths,
			OutputLimitKb:  int32(c.Constraints.OutputLimitKB),
		},
		Policy:        policyMetadataToProto(c.Policy),
		Observability: observabilityToProto(c.Observability),
	}
	for _, ex := range c.Examples {
		t.Examples = append(t.Examples, []byte(ex))
	}
	return t
}

func scopeToProto(s domain.Scope) pb.Scope {
	switch s {
	case domain.ScopeRepo:
		return pb.Scope_SCOPE_REPO
	case domain.ScopeWorkspace:
		return pb.Scope_SCOPE_WORKSPACE
	case domain.ScopeCluster:
		return pb.Scope_SCOPE_CLUSTER
	case domain.ScopeExternal:
		return pb.Scope_SCOPE_EXTERNAL
	default:
		return pb.Scope_SCOPE_UNSPECIFIED
	}
}

func sideEffectsToProto(s domain.SideEffects) pb.SideEffects {
	switch s {
	case domain.SideEffectsNone:
		return pb.SideEffects_SIDE_EFFECTS_NONE
	case domain.SideEffectsReversible:
		return pb.SideEffects_SIDE_EFFECTS_REVERSIBLE
	case domain.SideEffectsIrreversible:
		return pb.SideEffects_SIDE_EFFECTS_IRREVERSIBLE
	default:
		return pb.SideEffects_SIDE_EFFECTS_UNSPECIFIED
	}
}

func riskLevelToProto(r domain.RiskLevel) pb.RiskLevel {
	switch r {
	case domain.RiskLow:
		return pb.RiskLevel_RISK_LEVEL_LOW
	case domain.RiskMedium:
		return pb.RiskLevel_RISK_LEVEL_MEDIUM
	case domain.RiskHigh:
		return pb.RiskLevel_RISK_LEVEL_HIGH
	default:
		return pb.RiskLevel_RISK_LEVEL_UNSPECIFIED
	}
}

func idempotencyToProto(i domain.Idempotency) pb.Idempotency {
	switch i {
	case domain.IdempotencyGuaranteed:
		return pb.Idempotency_IDEMPOTENCY_GUARANTEED
	case domain.IdempotencyBestEffort:
		return pb.Idempotency_IDEMPOTENCY_BEST_EFFORT
	case domain.IdempotencyNone:
		return pb.Idempotency_IDEMPOTENCY_NONE
	default:
		return pb.Idempotency_IDEMPOTENCY_UNSPECIFIED
	}
}

func compactToolToProto(c app.CompactTool) *pb.CompactTool {
	return &pb.CompactTool{
		Name:         c.Name,
		Description:  c.Description,
		RequiredArgs: c.RequiredArgs,
		Risk:         c.Risk,
		SideEffects:  c.SideEffects,
		Approval:     c.Approval,
		Tags:         c.Tags,
		Cost:         c.Cost,
	}
}

func fullToolToProto(f app.FullTool) *pb.FullTool {
	ft := &pb.FullTool{
		Tool: toolToProto(f.Capability),
		Tags: f.Tags,
		Cost: f.Cost,
	}
	if f.Stats != nil {
		ft.Stats = &pb.ToolStats{
			SuccessRate:     f.Stats.SuccessRate,
			P50DurationMs:   f.Stats.P50Duration,
			P95DurationMs:   f.Stats.P95Duration,
			AvgOutputKb:     f.Stats.AvgOutputKB,
			DenyRate:        f.Stats.DenyRate,
			InvocationCount: int32(f.Stats.InvocationN),
		}
	}
	return ft
}

func recommendationToProto(r app.Recommendation) *pb.Recommendation {
	return &pb.Recommendation{
		Name:          r.Name,
		Score:         r.Score,
		Why:           r.Why,
		EstimatedCost: r.EstimatedCost,
		PolicyNotes:   r.PolicyNotes,
	}
}

func policyMetadataToProto(pm domain.PolicyMetadata) *pb.PolicyMetadata {
	p := &pb.PolicyMetadata{
		NamespaceFields: pm.NamespaceFields,
		RegistryFields:  pm.RegistryFields,
	}
	for _, f := range pm.PathFields {
		p.PathFields = append(p.PathFields, &pb.PathField{
			Field: f.Field, Multi: f.Multi, WorkspaceRelative: f.WorkspaceRelative,
		})
	}
	for i := range pm.ArgFields {
		f := &pm.ArgFields[i]
		p.ArgFields = append(p.ArgFields, &pb.ArgField{
			Field: f.Field, Multi: f.Multi,
			MaxItems: int32(f.MaxItems), MaxLength: int32(f.MaxLength),
			AllowedValues: f.AllowedValues, AllowedPrefix: f.AllowedPrefix,
			DeniedPrefix: f.DeniedPrefix, DenyCharacters: f.DenyCharacters,
		})
	}
	for _, f := range pm.ProfileFields {
		p.ProfileFields = append(p.ProfileFields, &pb.SimpleField{Field: f.Field, Multi: f.Multi})
	}
	for _, f := range pm.SubjectFields {
		p.SubjectFields = append(p.SubjectFields, &pb.SimpleField{Field: f.Field, Multi: f.Multi})
	}
	for _, f := range pm.TopicFields {
		p.TopicFields = append(p.TopicFields, &pb.SimpleField{Field: f.Field, Multi: f.Multi})
	}
	for _, f := range pm.QueueFields {
		p.QueueFields = append(p.QueueFields, &pb.SimpleField{Field: f.Field, Multi: f.Multi})
	}
	for _, f := range pm.KeyPrefixFields {
		p.KeyPrefixFields = append(p.KeyPrefixFields, &pb.SimpleField{Field: f.Field, Multi: f.Multi})
	}
	return p
}

func observabilityToProto(o domain.Observability) *pb.Observability {
	return &pb.Observability{
		TraceName: o.TraceName,
		SpanName:  o.SpanName,
	}
}

// ─── Error mapping ──────────────────────────────────────────────────────────

func serviceErrorToStatus(err *app.ServiceError) error {
	if err == nil {
		return nil
	}
	code := codes.Internal
	switch err.Code {
	case app.ErrorCodeInvalidArgument, app.ErrorCodeGitUsageError:
		code = codes.InvalidArgument
	case app.ErrorCodeNotFound:
		code = codes.NotFound
	case app.ErrorCodePolicyDenied:
		code = codes.PermissionDenied
	case app.ErrorCodeApprovalRequired:
		code = codes.FailedPrecondition
	case app.ErrorCodeTimeout:
		code = codes.DeadlineExceeded
	}
	return status.Error(code, err.Message)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func anyToProtoValue(v any) *structpb.Value {
	if v == nil {
		return structpb.NewNullValue()
	}
	// Marshal to JSON then back to structpb.Value to handle any type.
	data, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		return structpb.NewStringValue(marshalErr.Error())
	}
	var raw any
	if unmarshalErr := json.Unmarshal(data, &raw); unmarshalErr != nil {
		return structpb.NewStringValue(string(data))
	}
	val, valErr := structpb.NewValue(raw)
	if valErr != nil {
		return structpb.NewStringValue(string(data))
	}
	return val
}
