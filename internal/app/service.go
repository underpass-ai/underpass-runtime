package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	invocationOutputArtifactName = "invocation-output.json"
	invocationLogsArtifactName   = "invocation-logs.jsonl"
	sessionNotFound              = "session not found"
)

type Service struct {
	workspace       WorkspaceManager
	catalog         CapabilityRegistry
	policy          Authorizer
	tools           Invoker
	invStore        InvocationStore
	artifacts       ArtifactStore
	audit           AuditLogger
	events          EventPublisher
	telemetry       TelemetryRecorder
	telemetryQ      TelemetryQuerier
	kpiMetrics      *KPIMetrics
	quotas          *invocationQuotaLimiter
	metrics         *invocationMetrics
	qualityObserver QualityObserver
	policyLearned   PolicyReader
	neuralModel     NeuralModelReader
	decisionStore   RecommendationDecisionStore
	warmCaches      *sessionWarmCaches
	tracer          trace.Tracer

	// sessionInvCount tracks invocation count per session for first-tool metric.
	sessionInvCount sync.Map // sessionID → *int64
	// sessionLastRec tracks last recommendation tool IDs per session.
	sessionLastRec sync.Map // sessionID → []string
}

// NewService creates a workspace service wired to the given ports.
// An optional InvocationStore may be provided; if omitted, an in-memory
// store is used. Call Set* methods after construction to attach optional
// collaborators (event publisher, telemetry, session store, KPI metrics).
func NewService(
	workspace WorkspaceManager,
	catalog CapabilityRegistry,
	policy Authorizer,
	tools Invoker,
	artifacts ArtifactStore,
	audit AuditLogger,
	invStore ...InvocationStore,
) *Service {
	resolvedInvocationStore := InvocationStore(NewInMemoryInvocationStore())
	if len(invStore) > 0 && invStore[0] != nil {
		resolvedInvocationStore = invStore[0]
	}
	return &Service{
		workspace:       workspace,
		catalog:         catalog,
		policy:          policy,
		tools:           tools,
		invStore:        resolvedInvocationStore,
		artifacts:       artifacts,
		audit:           audit,
		events:          &noopEventPublisher{},
		telemetry:       noopTelemetryRecorder{},
		telemetryQ:      noopTelemetryQuerier{},
		quotas:          newInvocationQuotaLimiterFromEnv(),
		metrics:         newInvocationMetrics(),
		qualityObserver: noopQualityObserver{},
		decisionStore:   NewInMemoryRecommendationDecisionStore(),
		warmCaches:      newSessionWarmCaches(),
		tracer:          otel.Tracer("workspace.service"),
	}
}

// SetQualityObserver replaces the default noop quality observer.
func (s *Service) SetQualityObserver(obs QualityObserver) {
	if obs != nil {
		s.qualityObserver = obs
	}
}

// SetEventPublisher replaces the default noop event publisher.
func (s *Service) SetEventPublisher(pub EventPublisher) {
	if pub != nil {
		s.events = pub
	}
}

// SetTelemetry replaces the default noop telemetry recorder and querier.
func (s *Service) SetTelemetry(rec TelemetryRecorder, q TelemetryQuerier) {
	if rec != nil {
		s.telemetry = rec
	}
	if q != nil {
		s.telemetryQ = q
	}
}

// SetPolicyReader injects an optional learned-policy reader (Valkey).
func (s *Service) SetPolicyReader(pr PolicyReader) {
	if pr != nil {
		s.policyLearned = pr
	}
}

// SetRecommendationDecisionStore replaces the default in-memory decision store.
// SetNeuralModelReader injects an optional neural model reader.
func (s *Service) SetNeuralModelReader(r NeuralModelReader) {
	if r != nil {
		s.neuralModel = r
	}
}

func (s *Service) SetRecommendationDecisionStore(store RecommendationDecisionStore) {
	if store != nil {
		s.decisionStore = store
	}
}

// noopQualityObserver discards all quality metrics.
type noopQualityObserver struct{}

func (noopQualityObserver) ObserveInvocationQuality(context.Context, domain.InvocationQualityMetrics, domain.QualityObservationContext) {
}

// noopEventPublisher is the zero-dependency default that discards events.
type noopEventPublisher struct{}

func (noopEventPublisher) Publish(context.Context, domain.DomainEvent) error { return nil }

// noopTelemetryRecorder discards all telemetry records.
type noopTelemetryRecorder struct{}

func (noopTelemetryRecorder) Record(context.Context, TelemetryRecord) error { return nil }

// noopTelemetryQuerier returns empty stats.
type noopTelemetryQuerier struct{}

func (noopTelemetryQuerier) ToolStats(context.Context, string) (ToolStats, bool, error) {
	return ToolStats{}, false, nil
}
func (noopTelemetryQuerier) AllToolStats(context.Context) (map[string]ToolStats, error) {
	return nil, nil
}

func (s *Service) PrometheusMetrics() string {
	return renderPrometheusText(nil)
}

func (s *Service) CreateSession(ctx context.Context, req CreateSessionRequest) (domain.Session, *ServiceError) {
	if req.Principal.TenantID == "" {
		return domain.Session{}, invalidArgumentError("principal.tenant_id is required")
	}
	if req.Principal.ActorID == "" {
		return domain.Session{}, invalidArgumentError("principal.actor_id is required")
	}
	if req.ExpiresInSecond <= 0 {
		req.ExpiresInSecond = 3600
	}

	session, err := s.workspace.CreateSession(ctx, req)
	if err != nil {
		return domain.Session{}, internalError(err.Error())
	}
	s.publishEvent(ctx, domain.EventSessionCreated, session, domain.SessionCreatedPayload{
		RuntimeKind:  session.Runtime.Kind,
		RepoURL:      session.RepoURL,
		RepoRef:      session.RepoRef,
		ExpiresAt:    session.ExpiresAt,
		WorkspaceDir: session.WorkspacePath,
	})
	if s.kpiMetrics != nil {
		s.kpiMetrics.ObserveSessionCreated()
	}
	s.prewarmSession(session)
	return session, nil
}

func (s *Service) CloseSession(ctx context.Context, sessionID string) *ServiceError {
	if sessionID == "" {
		return invalidArgumentError("session_id is required")
	}
	session, found, _ := s.workspace.GetSession(ctx, sessionID)
	if err := s.workspace.CloseSession(ctx, sessionID); err != nil {
		return internalError(err.Error())
	}
	if found {
		durationSec := int64(time.Since(session.CreatedAt).Seconds())
		s.publishEvent(ctx, domain.EventSessionClosed, session, domain.SessionClosedPayload{
			RuntimeKind: session.Runtime.Kind,
			DurationSec: durationSec,
		})
	}
	if s.kpiMetrics != nil {
		s.kpiMetrics.ObserveSessionClosed()
	}
	s.warmCaches.evict(sessionID)
	return nil
}

func (s *Service) ValidateSessionAccess(ctx context.Context, sessionID string, principal domain.Principal) *ServiceError {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return invalidArgumentError("session_id is required")
	}

	session, found, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil {
		return internalError(err.Error())
	}
	if !found {
		return notFoundError(sessionNotFound)
	}
	if !samePrincipalIdentity(session.Principal, principal) {
		return policyDeniedError(ErrorCodePolicyDenied, "session does not belong to authenticated principal")
	}
	return nil
}

func (s *Service) ValidateInvocationAccess(ctx context.Context, invocationID string, principal domain.Principal) *ServiceError {
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return invalidArgumentError("invocation_id is required")
	}

	invocation, found, err := s.invStore.Get(ctx, invocationID)
	if err != nil {
		return internalError(err.Error())
	}
	if !found {
		return notFoundError("invocation not found")
	}
	return s.ValidateSessionAccess(ctx, invocation.SessionID, principal)
}

func (s *Service) ListTools(ctx context.Context, sessionID string) ([]domain.Capability, *ServiceError) {
	session, found, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil {
		return nil, internalError(err.Error())
	}
	if !found {
		return nil, notFoundError(sessionNotFound)
	}

	all := s.catalog.List()
	enabled := make([]domain.Capability, 0, len(all))
	for _, capability := range all {
		if !capabilitySupportedByRuntime(session, capability) {
			continue
		}
		decision, decisionErr := s.policy.Authorize(ctx, PolicyInput{
			Session:    session,
			Capability: capability,
			Args:       json.RawMessage("{}"),
			Approved:   true,
		})
		if decisionErr != nil {
			return nil, internalError(decisionErr.Error())
		}
		if decision.Allow {
			enabled = append(enabled, capability)
		}
	}

	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].Name < enabled[j].Name
	})
	return enabled, nil
}

func (s *Service) InvokeTool(ctx context.Context, sessionID, toolName string, req InvokeToolRequest) (domain.Invocation, *ServiceError) {
	session, capability, existing, valErr := s.validateToolRequest(ctx, sessionID, toolName, req.CorrelationID)
	if valErr != nil {
		return domain.Invocation{}, valErr
	}
	if existing != nil {
		return *existing, nil
	}

	invocationID := newID("inv")
	startedAt := time.Now().UTC()
	invocation := domain.Invocation{
		ID:            invocationID,
		SessionID:     sessionID,
		ToolName:      toolName,
		CorrelationID: strings.TrimSpace(req.CorrelationID),
		Status:        domain.InvocationStatusRunning,
		StartedAt:     startedAt,
		TraceName:     capability.Observability.TraceName,
		SpanName:      capability.Observability.SpanName,
	}
	spanName := strings.TrimSpace(capability.Observability.SpanName)
	if spanName == "" {
		spanName = toolName
	}
	ctx, span := s.tracer.Start(ctx, spanName, trace.WithAttributes(
		attribute.String("workspace.tool", toolName),
		attribute.String("workspace.session_id", sessionID),
		attribute.String("workspace.invocation_id", invocationID),
		attribute.String("workspace.correlation_id", invocation.CorrelationID),
		attribute.String("workspace.trace_name", capability.Observability.TraceName),
	))
	defer func() {
		finaliseInvocationSpan(span, invocation)
	}()
	recordMetrics := false
	defer func() {
		if recordMetrics && s.metrics != nil {
			s.metrics.Observe(invocation)
		}
		if recordMetrics && s.qualityObserver != nil {
			if qm, qerr := domain.ComputeInvocationQuality(invocation); qerr == nil {
				s.qualityObserver.ObserveInvocationQuality(ctx, qm, domain.QualityObservationContext{
					SessionID: session.ID,
					TenantID:  session.Principal.TenantID,
					ActorID:   session.Principal.ActorID,
					Timestamp: time.Now().UTC(),
				})
			}
		}
	}()
	if serviceErr := s.storeInvocation(ctx, invocation); serviceErr != nil {
		return invocation, serviceErr
	}
	s.publishEvent(ctx, domain.EventInvocationStarted, session, domain.InvocationStartedPayload{
		InvocationID:  invocationID,
		ToolName:      toolName,
		CorrelationID: invocation.CorrelationID,
	})

	invocation, releaseConcurrency, authErr := s.authorizeToolInvocation(ctx, invocation, startedAt, session, capability, req.Args, req.Approved)
	if authErr != nil {
		recordMetrics = true
		return invocation, authErr
	}
	defer releaseConcurrency()

	toolCtx := ctx
	cancel := func() { /* no-op; replaced below if timeout is set */ }
	if capability.Constraints.TimeoutSeconds > 0 {
		toolCtx, cancel = context.WithTimeout(ctx, time.Duration(capability.Constraints.TimeoutSeconds)*time.Second)
	}
	defer cancel()

	runResult, runErr := s.tools.Invoke(toolCtx, session, capability, req.Args)

	if runErr == nil {
		if allowed, reason := s.quotas.allowRunResult(runResult); !allowed {
			invocation = s.denyInvocation(ctx, invocation, startedAt, session, &domain.Error{Code: ErrorCodePolicyDenied, Message: reason, Retryable: true})
			recordMetrics = true
			return invocation, policyDeniedError(ErrorCodePolicyDenied, reason)
		}
	}

	invocation.Output = runResult.Output
	invocation.Logs = runResult.Logs
	invocation.ExitCode = runResult.ExitCode

	invocation, svcErr := s.completeToolInvocation(ctx, toolCtx, invocation, toolCompletionContext{
		startedAt:  startedAt,
		session:    session,
		capability: capability,
		runResult:  runResult,
		runErr:     runErr,
	})
	recordMetrics = true
	return invocation, svcErr
}

// validateToolRequest validates session exists, tool exists, and checks for
// an existing deduplicated invocation by correlation ID.
func (s *Service) validateToolRequest(ctx context.Context, sessionID, toolName, correlationID string) (domain.Session, domain.Capability, *domain.Invocation, *ServiceError) {
	session, found, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, domain.Capability{}, nil, internalError(err.Error())
	}
	if !found {
		return domain.Session{}, domain.Capability{}, nil, notFoundError(sessionNotFound)
	}
	capability, ok := s.catalog.Get(toolName)
	if !ok {
		return domain.Session{}, domain.Capability{}, nil, notFoundError("tool not found")
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID != "" {
		if existing, ok, serviceErr := s.findInvocationByCorrelation(ctx, sessionID, toolName, correlationID); serviceErr != nil {
			return domain.Session{}, domain.Capability{}, nil, serviceErr
		} else if ok {
			return session, capability, &existing, nil
		}
	}
	return session, capability, nil, nil
}

// authorizeToolInvocation runs rate-limit, runtime-support, policy, and
// concurrency checks. On denial it marks the invocation and returns a
// non-nil ServiceError. On success it returns the release function for the
// acquired concurrency slot.
func (s *Service) authorizeToolInvocation(
	ctx context.Context, inv domain.Invocation, startedAt time.Time,
	session domain.Session, capability domain.Capability,
	args json.RawMessage, approved bool,
) (domain.Invocation, func(), *ServiceError) {
	noop := func() { /* no concurrency slot acquired */ }
	if allowed, reason := s.quotas.allowRate(session, startedAt); !allowed {
		inv = s.denyInvocation(ctx, inv, startedAt, session, &domain.Error{Code: ErrorCodePolicyDenied, Message: reason, Retryable: true})
		return inv, noop, policyDeniedError(ErrorCodePolicyDenied, reason)
	}
	if !capabilitySupportedByRuntime(session, capability) {
		reason := unsupportedRuntimeReason(session, capability)
		inv = s.denyInvocation(ctx, inv, startedAt, session, &domain.Error{Code: ErrorCodePolicyDenied, Message: reason, Retryable: false})
		return inv, noop, policyDeniedError(ErrorCodePolicyDenied, reason)
	}
	decision, decisionErr := s.policy.Authorize(ctx, PolicyInput{
		Session:    session,
		Capability: capability,
		Args:       args,
		Approved:   approved,
	})
	if decisionErr != nil {
		inv = s.finishWithError(inv, startedAt, &domain.Error{Code: ErrorCodeInternal, Message: decisionErr.Error(), Retryable: false})
		_ = s.storeInvocation(ctx, inv)
		s.audit.Record(ctx, auditEventFromInvocation(session, inv))
		return inv, noop, internalError(decisionErr.Error())
	}
	if !decision.Allow {
		code := decision.ErrorCode
		if code == "" {
			code = ErrorCodePolicyDenied
		}
		inv = s.denyInvocation(ctx, inv, startedAt, session, &domain.Error{Code: code, Message: decision.Reason, Retryable: false})
		return inv, noop, policyDeniedError(code, decision.Reason)
	}
	releaseConcurrency, acquired := s.quotas.acquireConcurrency(inv.SessionID)
	if !acquired {
		reason := "session invocation concurrency limit exceeded"
		inv = s.denyInvocation(ctx, inv, startedAt, session, &domain.Error{Code: ErrorCodePolicyDenied, Message: reason, Retryable: true})
		return inv, noop, policyDeniedError(ErrorCodePolicyDenied, reason)
	}
	return inv, releaseConcurrency, nil
}

// toolCompletionContext bundles the parameters needed by completeToolInvocation
// so the method stays within the 7-parameter limit.
type toolCompletionContext struct {
	startedAt  time.Time
	session    domain.Session
	capability domain.Capability
	runResult  ToolRunResult
	runErr     *domain.Error
}

// completeToolInvocation persists run artifacts, handles run/artifact/schema
// errors, and marks the invocation as succeeded when everything passes.
func (s *Service) completeToolInvocation(
	ctx context.Context, toolCtx context.Context, inv domain.Invocation, tc toolCompletionContext,
) (domain.Invocation, *ServiceError) {
	artifacts, outputRef, logsRef, artifactErr := s.persistRunArtifacts(ctx, inv.ID, tc.runResult)
	if artifactErr == nil {
		inv.Artifacts = artifacts
		inv.OutputRef = outputRef
		inv.LogsRef = logsRef
	}
	if tc.runErr != nil {
		if IsPolicyDeniedCode(tc.runErr.Code) {
			inv = s.denyInvocation(ctx, inv, tc.startedAt, tc.session, tc.runErr)
			return inv, policyDeniedError(tc.runErr.Code, tc.runErr.Message)
		}
		inv = s.finishWithError(inv, tc.startedAt, tc.runErr)
		_ = s.storeInvocation(ctx, inv)
		s.audit.Record(ctx, auditEventFromInvocation(tc.session, inv))
		s.publishInvocationCompleted(ctx, tc.session, inv)
		s.recordTelemetry(ctx, tc.session, inv, tc.runResult)
		return inv, runServiceError(toolCtx, tc.runErr)
	}
	if artifactErr != nil {
		inv = s.finishWithError(inv, tc.startedAt, &domain.Error{Code: ErrorCodeInternal, Message: artifactErr.Error(), Retryable: false})
		_ = s.storeInvocation(ctx, inv)
		s.audit.Record(ctx, auditEventFromInvocation(tc.session, inv))
		s.publishInvocationCompleted(ctx, tc.session, inv)
		s.recordTelemetry(ctx, tc.session, inv, tc.runResult)
		return inv, internalError(artifactErr.Error())
	}
	if validationErr := validateOutputAgainstSchema(tc.capability.OutputSchema, tc.runResult.Output); validationErr != nil {
		inv = s.finishWithError(inv, tc.startedAt, &domain.Error{Code: ErrorCodeInternal, Message: validationErr.Error(), Retryable: false})
		_ = s.storeInvocation(ctx, inv)
		s.audit.Record(ctx, auditEventFromInvocation(tc.session, inv))
		s.publishInvocationCompleted(ctx, tc.session, inv)
		s.recordTelemetry(ctx, tc.session, inv, tc.runResult)
		return inv, internalError(validationErr.Error())
	}
	endedAt := time.Now().UTC()
	inv.Status = domain.InvocationStatusSucceeded
	inv.CompletedAt = &endedAt
	inv.DurationMS = endedAt.Sub(tc.startedAt).Milliseconds()
	if serviceErr := s.storeInvocation(ctx, inv); serviceErr != nil {
		return inv, serviceErr
	}
	s.audit.Record(ctx, auditEventFromInvocation(tc.session, inv))
	s.publishInvocationCompleted(ctx, tc.session, inv)
	s.recordTelemetry(ctx, tc.session, inv, tc.runResult)
	s.observeToolKPIs(tc.session.ID, inv.ToolName, inv.Status == domain.InvocationStatusSucceeded)
	return inv, nil
}

// denyInvocation marks the invocation as denied, persists it, records the
// audit event, and returns the updated invocation value.
func (s *Service) denyInvocation(ctx context.Context, invocation domain.Invocation, startedAt time.Time, session domain.Session, domErr *domain.Error) domain.Invocation {
	invocation.Status = domain.InvocationStatusDenied
	invocation = s.finishWithError(invocation, startedAt, domErr)
	_ = s.storeInvocation(ctx, invocation)
	s.audit.Record(ctx, auditEventFromInvocation(session, invocation))
	s.recordTelemetry(ctx, session, invocation, ToolRunResult{})
	s.publishEvent(ctx, domain.EventInvocationDenied, session, domain.InvocationDeniedPayload{
		InvocationID:  invocation.ID,
		ToolName:      invocation.ToolName,
		CorrelationID: invocation.CorrelationID,
		Reason:        domErr.Message,
	})
	if s.kpiMetrics != nil {
		reason := domErr.Code
		if reason == "" {
			reason = domErr.Message
		}
		s.kpiMetrics.ObserveInvocationDenied(reason)
		s.kpiMetrics.ObservePolicyDenialAfterRecommendation(s.wasRecommended(session.ID, invocation.ToolName))
	}
	return invocation
}

// observeToolKPIs records tool-call and first-tool-result KPI metrics.
func (s *Service) observeToolKPIs(sessionID, toolName string, succeeded bool) {
	if s.kpiMetrics == nil {
		return
	}
	s.kpiMetrics.ObserveToolCall(toolName)
	s.kpiMetrics.ObserveRecommendationUsed(s.wasRecommended(sessionID, toolName))

	// First tool result tracking.
	countPtr := new(int64)
	if v, loaded := s.sessionInvCount.LoadOrStore(sessionID, countPtr); loaded {
		countPtr = v.(*int64)
	}
	n := atomic.AddInt64(countPtr, 1)
	if n == 1 {
		s.kpiMetrics.ObserveFirstToolResult(succeeded)
	}
}

// wasRecommended checks if a tool was in the last recommendation for a session.
func (s *Service) wasRecommended(sessionID, toolName string) bool {
	v, ok := s.sessionLastRec.Load(sessionID)
	if !ok {
		return false
	}
	for _, id := range v.([]string) {
		if id == toolName {
			return true
		}
	}
	return false
}

// finaliseInvocationSpan annotates span with the final invocation state and
// ends it. It is called from a defer so it captures invocation by reference
// via the pointer in the closure.
func finaliseInvocationSpan(span trace.Span, invocation domain.Invocation) {
	if invocation.Status != "" {
		span.SetAttributes(attribute.String("workspace.invocation_status", string(invocation.Status)))
	}
	if invocation.DurationMS >= 0 {
		span.SetAttributes(attribute.Int64("workspace.duration_ms", invocation.DurationMS))
	}
	if invocation.Error != nil {
		if code := strings.TrimSpace(invocation.Error.Code); code != "" {
			span.SetAttributes(attribute.String("workspace.error_code", code))
		}
		if message := strings.TrimSpace(invocation.Error.Message); message != "" {
			span.RecordError(errors.New(message))
			span.SetStatus(codes.Error, message)
		} else {
			span.SetStatus(codes.Error, "invocation failed")
		}
	} else if invocation.Status == domain.InvocationStatusSucceeded {
		span.SetStatus(codes.Ok, "succeeded")
	}
	span.End()
}

func runServiceError(toolCtx context.Context, runErr *domain.Error) *ServiceError {
	if errors.Is(toolCtx.Err(), context.DeadlineExceeded) || runErr.Code == ErrorCodeTimeout {
		return &ServiceError{Code: runErr.Code, Message: runErr.Message, HTTPStatus: 504}
	}
	return &ServiceError{Code: runErr.Code, Message: runErr.Message, HTTPStatus: 500}
}

func (s *Service) GetInvocation(ctx context.Context, invocationID string) (domain.Invocation, *ServiceError) {
	invocation, found, err := s.invStore.Get(ctx, invocationID)
	if err != nil {
		return domain.Invocation{}, internalError(err.Error())
	}
	if !found {
		return domain.Invocation{}, notFoundError("invocation not found")
	}
	if err := s.hydrateOutputByRef(ctx, &invocation); err != nil {
		return domain.Invocation{}, internalError(err.Error())
	}
	return invocation, nil
}

func (s *Service) GetInvocationLogs(ctx context.Context, invocationID string) ([]domain.LogLine, *ServiceError) {
	invocation, serviceErr := s.GetInvocation(ctx, invocationID)
	if serviceErr != nil {
		return nil, serviceErr
	}
	if len(invocation.Logs) > 0 || invocation.LogsRef == "" {
		return invocation.Logs, nil
	}

	logs, err := s.loadLogsByRef(ctx, &invocation, invocation.LogsRef)
	if err != nil {
		return nil, internalError(err.Error())
	}
	return logs, nil
}

func (s *Service) GetInvocationArtifacts(ctx context.Context, invocationID string) ([]domain.Artifact, *ServiceError) {
	invocation, serviceErr := s.GetInvocation(ctx, invocationID)
	if serviceErr != nil {
		return nil, serviceErr
	}
	if len(invocation.Artifacts) > 0 {
		return invocation.Artifacts, nil
	}

	artifacts, err := s.artifacts.List(ctx, invocationID)
	if err != nil {
		return nil, internalError(err.Error())
	}
	return artifacts, nil
}

func (s *Service) storeInvocation(ctx context.Context, invocation domain.Invocation) *ServiceError {
	if err := s.invStore.Save(ctx, invocation); err != nil {
		return internalError(err.Error())
	}
	return nil
}

func (s *Service) finishWithError(invocation domain.Invocation, startedAt time.Time, err *domain.Error) domain.Invocation {
	endedAt := time.Now().UTC()
	if invocation.Status != domain.InvocationStatusDenied {
		invocation.Status = domain.InvocationStatusFailed
	}
	invocation.CompletedAt = &endedAt
	invocation.DurationMS = endedAt.Sub(startedAt).Milliseconds()
	invocation.Error = err
	return invocation
}

func (s *Service) findInvocationByCorrelation(
	ctx context.Context,
	sessionID string,
	toolName string,
	correlationID string,
) (domain.Invocation, bool, *ServiceError) {
	lookupStore, ok := s.invStore.(CorrelationFinder)
	if !ok {
		return domain.Invocation{}, false, nil
	}
	invocation, found, err := lookupStore.FindByCorrelation(ctx, sessionID, toolName, correlationID)
	if err != nil {
		return domain.Invocation{}, false, internalError(err.Error())
	}
	return invocation, found, nil
}

func (s *Service) persistRunArtifacts(
	ctx context.Context,
	invocationID string,
	runResult ToolRunResult,
) ([]domain.Artifact, string, string, error) {
	payloads, err := buildInvocationArtifactPayloads(runResult)
	if err != nil {
		return nil, "", "", err
	}
	if len(payloads) == 0 {
		return nil, "", "", nil
	}

	artifacts, err := s.artifacts.Save(ctx, invocationID, payloads)
	if err != nil {
		return nil, "", "", err
	}
	return artifacts, findArtifactIDByName(artifacts, invocationOutputArtifactName), findArtifactIDByName(artifacts, invocationLogsArtifactName), nil
}

func buildInvocationArtifactPayloads(runResult ToolRunResult) ([]ArtifactPayload, error) {
	payloads := make([]ArtifactPayload, 0, len(runResult.Artifacts)+2)
	payloads = append(payloads, runResult.Artifacts...)

	if runResult.Output != nil {
		outputData, err := json.Marshal(runResult.Output)
		if err != nil {
			return nil, fmt.Errorf("marshal invocation output artifact: %w", err)
		}
		payloads = append(payloads, ArtifactPayload{
			Name:        invocationOutputArtifactName,
			ContentType: "application/json",
			Data:        outputData,
		})
	}

	if len(runResult.Logs) > 0 {
		var logsBuffer bytes.Buffer
		encoder := json.NewEncoder(&logsBuffer)
		for _, line := range runResult.Logs {
			if err := encoder.Encode(line); err != nil {
				return nil, fmt.Errorf("marshal invocation logs artifact: %w", err)
			}
		}
		payloads = append(payloads, ArtifactPayload{
			Name:        invocationLogsArtifactName,
			ContentType: "application/x-ndjson",
			Data:        logsBuffer.Bytes(),
		})
	}

	return payloads, nil
}

func findArtifactIDByName(artifacts []domain.Artifact, name string) string {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact.ID
		}
	}
	return ""
}

func (s *Service) hydrateOutputByRef(ctx context.Context, invocation *domain.Invocation) error {
	if invocation.Output != nil || strings.TrimSpace(invocation.OutputRef) == "" {
		return nil
	}
	payload, err := s.readArtifactByRef(ctx, invocation, invocation.OutputRef)
	if err != nil {
		return err
	}
	var output any
	if err := json.Unmarshal(payload, &output); err != nil {
		return fmt.Errorf("unmarshal invocation output artifact: %w", err)
	}
	invocation.Output = output
	return nil
}

func (s *Service) loadLogsByRef(ctx context.Context, invocation *domain.Invocation, logsRef string) ([]domain.LogLine, error) {
	payload, err := s.readArtifactByRef(ctx, invocation, logsRef)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(payload, []byte("\n"))
	out := make([]domain.LogLine, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var item domain.LogLine
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("unmarshal invocation log line: %w", err)
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) readArtifactByRef(ctx context.Context, invocation *domain.Invocation, artifactID string) ([]byte, error) {
	artifact, err := s.resolveArtifact(ctx, invocation, artifactID)
	if err != nil {
		return nil, err
	}
	data, err := s.artifacts.Read(ctx, artifact.Path)
	if err != nil {
		return nil, fmt.Errorf("read artifact %s: %w", artifactID, err)
	}
	return data, nil
}

func (s *Service) resolveArtifact(ctx context.Context, invocation *domain.Invocation, artifactID string) (domain.Artifact, error) {
	for _, artifact := range invocation.Artifacts {
		if artifact.ID == artifactID {
			return artifact, nil
		}
	}
	artifacts, err := s.artifacts.List(ctx, invocation.ID)
	if err != nil {
		return domain.Artifact{}, err
	}
	invocation.Artifacts = artifacts
	for _, artifact := range artifacts {
		if artifact.ID == artifactID {
			return artifact, nil
		}
	}
	return domain.Artifact{}, fmt.Errorf("artifact ref not found: %s", artifactID)
}

type outputSchema struct {
	Type       string                          `json:"type"`
	Required   []string                        `json:"required,omitempty"`
	Properties map[string]outputSchemaProperty `json:"properties,omitempty"`
}

type outputSchemaProperty struct {
	Type string `json:"type"`
}

func validateOutputAgainstSchema(schemaRaw json.RawMessage, output any) error {
	if len(schemaRaw) == 0 || string(schemaRaw) == "null" {
		return nil
	}

	var schema outputSchema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("invalid output schema: %w", err)
	}
	if schema.Type == "" {
		return nil
	}
	if !matchesSchemaType(output, schema.Type) {
		return fmt.Errorf("tool output type mismatch: expected %s", schema.Type)
	}

	if schema.Type != "object" {
		return nil
	}
	objectValue, ok := output.(map[string]any)
	if !ok {
		return fmt.Errorf("tool output must be an object")
	}
	if err := validateRequiredFields(objectValue, schema.Required); err != nil {
		return err
	}
	return validateSchemaProperties(objectValue, schema.Properties)
}

func validateRequiredFields(obj map[string]any, required []string) error {
	for _, field := range required {
		if _, exists := obj[field]; !exists {
			return fmt.Errorf("tool output missing required field: %s", field)
		}
	}
	return nil
}

func validateSchemaProperties(obj map[string]any, properties map[string]outputSchemaProperty) error {
	for key, property := range properties {
		if property.Type == "" {
			continue
		}
		value, exists := obj[key]
		if !exists {
			continue
		}
		if !matchesSchemaType(value, property.Type) {
			return fmt.Errorf("tool output field %s has invalid type", key)
		}
	}
	return nil
}

func matchesSchemaType(value any, schemaType string) bool {
	switch schemaType {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		if value == nil {
			return false
		}
		kind := reflect.TypeOf(value).Kind()
		return kind == reflect.Slice || kind == reflect.Array
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float32:
			return math.Trunc(float64(typed)) == float64(typed)
		case float64:
			return math.Trunc(typed) == typed
		default:
			return false
		}
	case "number":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func auditEventFromInvocation(session domain.Session, invocation domain.Invocation) AuditEvent {
	return AuditEvent{
		At:            time.Now().UTC(),
		SessionID:     session.ID,
		ToolName:      invocation.ToolName,
		InvocationID:  invocation.ID,
		CorrelationID: invocation.CorrelationID,
		Status:        string(invocation.Status),
		ActorID:       session.Principal.ActorID,
		TenantID:      session.Principal.TenantID,
		Metadata:      session.Metadata,
	}
}

func capabilitySupportedByRuntime(session domain.Session, capability domain.Capability) bool {
	if capability.Scope != domain.ScopeCluster {
		return true
	}
	if session.Runtime.Kind != domain.RuntimeKindKubernetes {
		return false
	}
	if isK8sDeliveryCapability(capability.Name) {
		return envBool("WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS", false)
	}
	return true
}

func unsupportedRuntimeReason(session domain.Session, capability domain.Capability) string {
	if capability.Scope == domain.ScopeCluster {
		if session.Runtime.Kind == "" || session.Runtime.Kind == domain.RuntimeKindLocal {
			return "tool requires kubernetes runtime"
		}
		if isK8sDeliveryCapability(capability.Name) && !envBool("WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS", false) {
			return "k8s delivery tools are disabled by configuration"
		}
		return fmt.Sprintf("tool requires kubernetes runtime (session runtime=%s)", session.Runtime.Kind)
	}
	return "tool is not supported by current runtime"
}

func isK8sDeliveryCapability(name string) bool {
	switch strings.TrimSpace(name) {
	case "k8s.apply_manifest", "k8s.rollout_status", "k8s.restart_deployment", "k8s.rollout_pause", "k8s.rollout_undo":
		return true
	default:
		return false
	}
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

type invocationQuotaLimiter struct {
	mu                         sync.Mutex
	maxPerMinute               int
	maxPerMinutePerPrincipal   int
	maxConcurrentPerSession    int
	maxOutputBytes             int
	maxArtifactsPerInvoke      int
	maxArtifactBytesPerInvoke  int
	perSessionWindowCounters   map[string]quotaWindow
	perPrincipalWindowCounters map[string]quotaWindow
	perSessionInFlight         map[string]int
}

type quotaWindow struct {
	start time.Time
	count int
}

func newInvocationQuotaLimiterFromEnv() *invocationQuotaLimiter {
	return &invocationQuotaLimiter{
		maxPerMinute:               envInt("WORKSPACE_RATE_LIMIT_PER_MINUTE", 0),
		maxPerMinutePerPrincipal:   envInt("WORKSPACE_RATE_LIMIT_PER_MINUTE_PER_PRINCIPAL", 0),
		maxConcurrentPerSession:    envInt("WORKSPACE_MAX_CONCURRENCY_PER_SESSION", 0),
		maxOutputBytes:             envInt("WORKSPACE_MAX_OUTPUT_BYTES_PER_INVOCATION", 0),
		maxArtifactsPerInvoke:      envInt("WORKSPACE_MAX_ARTIFACTS_PER_INVOCATION", 0),
		maxArtifactBytesPerInvoke:  envInt("WORKSPACE_MAX_ARTIFACT_BYTES_PER_INVOCATION", 0),
		perSessionWindowCounters:   map[string]quotaWindow{},
		perPrincipalWindowCounters: map[string]quotaWindow{},
		perSessionInFlight:         map[string]int{},
	}
}

func (l *invocationQuotaLimiter) allowRate(session domain.Session, now time.Time) (bool, string) {
	if l == nil {
		return true, ""
	}

	checkSessionRate := l.maxPerMinute > 0
	checkPrincipalRate := l.maxPerMinutePerPrincipal > 0
	if !checkSessionRate && !checkPrincipalRate {
		return true, ""
	}

	windowStart := now.Truncate(time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()

	if checkSessionRate {
		allowed := incrementQuotaWindowCounter(
			l.perSessionWindowCounters,
			strings.TrimSpace(session.ID),
			windowStart,
			l.maxPerMinute,
		)
		if !allowed {
			return false, "session invocation rate limit exceeded"
		}
	}

	if checkPrincipalRate {
		principalKey := principalQuotaKey(session.Principal)
		if principalKey == "" {
			principalKey = strings.TrimSpace(session.ID)
		}
		allowed := incrementQuotaWindowCounter(
			l.perPrincipalWindowCounters,
			principalKey,
			windowStart,
			l.maxPerMinutePerPrincipal,
		)
		if !allowed {
			return false, "principal invocation rate limit exceeded"
		}
	}
	return true, ""
}

func incrementQuotaWindowCounter(
	counters map[string]quotaWindow,
	key string,
	windowStart time.Time,
	maxPerMinute int,
) bool {
	if maxPerMinute <= 0 {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}

	window := counters[key]
	if window.start.IsZero() || !window.start.Equal(windowStart) {
		window = quotaWindow{
			start: windowStart,
			count: 0,
		}
	}
	if window.count >= maxPerMinute {
		counters[key] = window
		return false
	}
	window.count++
	counters[key] = window
	return true
}

func principalQuotaKey(principal domain.Principal) string {
	tenantID := strings.TrimSpace(principal.TenantID)
	actorID := strings.TrimSpace(principal.ActorID)
	if tenantID == "" && actorID == "" {
		return ""
	}
	return strings.ToLower(tenantID) + ":" + strings.ToLower(actorID)
}

func (l *invocationQuotaLimiter) allowRunResult(result ToolRunResult) (bool, string) {
	if l == nil {
		return true, ""
	}

	if l.maxOutputBytes > 0 && result.Output != nil {
		payload, err := json.Marshal(result.Output)
		if err == nil && len(payload) > l.maxOutputBytes {
			return false, "invocation output size quota exceeded"
		}
	}

	if l.maxArtifactsPerInvoke > 0 && len(result.Artifacts) > l.maxArtifactsPerInvoke {
		return false, "invocation artifact count quota exceeded"
	}

	if l.maxArtifactBytesPerInvoke > 0 {
		totalBytes := 0
		for _, artifact := range result.Artifacts {
			totalBytes += len(artifact.Data)
			if totalBytes > l.maxArtifactBytesPerInvoke {
				return false, "invocation artifact size quota exceeded"
			}
		}
	}

	return true, ""
}

func (l *invocationQuotaLimiter) acquireConcurrency(sessionID string) (func(), bool) {
	if l == nil || l.maxConcurrentPerSession <= 0 {
		return func() { /* no-op release; no concurrency limit enforced */ }, true
	}

	l.mu.Lock()
	current := l.perSessionInFlight[sessionID]
	if current >= l.maxConcurrentPerSession {
		l.mu.Unlock()
		return nil, false
	}
	l.perSessionInFlight[sessionID] = current + 1
	l.mu.Unlock()

	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		remaining := l.perSessionInFlight[sessionID] - 1
		if remaining <= 0 {
			delete(l.perSessionInFlight, sessionID)
			return
		}
		l.perSessionInFlight[sessionID] = remaining
	}, true
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < 0 {
		return 0
	}
	return value
}

func samePrincipalIdentity(expected, actual domain.Principal) bool {
	return strings.TrimSpace(expected.TenantID) == strings.TrimSpace(actual.TenantID) &&
		strings.TrimSpace(expected.ActorID) == strings.TrimSpace(actual.ActorID)
}

// recordTelemetry captures a telemetry record for a completed invocation.
// Errors are silently ignored — telemetry must not block the primary operation.
func (s *Service) recordTelemetry(ctx context.Context, session domain.Session, inv domain.Invocation, runResult ToolRunResult) {
	var outputBytes int64
	if inv.Output != nil {
		if data, marshalErr := json.Marshal(inv.Output); marshalErr == nil {
			outputBytes = int64(len(data))
		}
	}
	var logsBytes int64
	for i := range inv.Logs {
		logsBytes += int64(len(inv.Logs[i].Message))
	}
	var artifactBytes int64
	for i := range runResult.Artifacts {
		artifactBytes += int64(len(runResult.Artifacts[i].Data))
	}
	errorCode := ""
	if inv.Error != nil {
		errorCode = inv.Error.Code
	}
	toolFamily := ""
	if idx := strings.IndexByte(inv.ToolName, '.'); idx > 0 {
		toolFamily = inv.ToolName[:idx]
	}
	// Derive context signal for learning pipeline consumption.
	digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
	contextSig := DeriveContextSignature(session, inv.ToolName, digest)
	approved := inv.Status != domain.InvocationStatusDenied

	rec := TelemetryRecord{
		InvocationID:  inv.ID,
		SessionID:     session.ID,
		ToolName:      inv.ToolName,
		ToolFamily:    toolFamily,
		RuntimeKind:   string(session.Runtime.Kind),
		RepoLanguage:  digest.RepoLanguage,
		ProjectType:   digest.ProjectType,
		TenantID:      session.Principal.TenantID,
		Approved:      approved,
		Status:        string(inv.Status),
		ErrorCode:     errorCode,
		ContextSig:    contextSig,
		DurationMs:    inv.DurationMS,
		OutputBytes:   outputBytes,
		LogsBytes:     logsBytes,
		ArtifactCount: len(inv.Artifacts),
		ArtifactBytes: artifactBytes,
		Timestamp:     time.Now().UTC(),
	}
	_ = s.telemetry.Record(ctx, rec)
}

// publishEvent builds a DomainEvent and publishes it. Errors are silently
// ignored — event publishing must not block the primary operation.
func (s *Service) publishEvent(ctx context.Context, eventType domain.EventType, session domain.Session, payload any) {
	evt, err := domain.NewDomainEvent(newID("evt"), eventType, session.ID, session.Principal.TenantID, session.Principal.ActorID, payload)
	if err != nil {
		return
	}
	_ = s.events.Publish(ctx, evt)
}

// publishInvocationCompleted publishes an EventInvocationCompleted with output size estimation.
func (s *Service) publishInvocationCompleted(ctx context.Context, session domain.Session, inv domain.Invocation) {
	var outputBytes int64
	if inv.Output != nil {
		if data, err := json.Marshal(inv.Output); err == nil {
			outputBytes = int64(len(data))
		}
	}
	s.publishEvent(ctx, domain.EventInvocationCompleted, session, domain.InvocationCompletedPayload{
		InvocationID:  inv.ID,
		ToolName:      inv.ToolName,
		CorrelationID: inv.CorrelationID,
		Status:        inv.Status,
		ExitCode:      inv.ExitCode,
		DurationMS:    inv.DurationMS,
		OutputBytes:   outputBytes,
		ArtifactCount: len(inv.Artifacts),
	})
}

// ─── Learning Evidence P0 ──────────────────────────────────────────────────

// EvidenceBundle is the compact audit package returned by GetEvidenceBundle.
type EvidenceBundle struct {
	Recommendation domain.RecommendationDecision `json:"recommendation"`
	Policy         *ToolPolicy                   `json:"policy,omitempty"`
	Aggregate      *ToolStats                    `json:"aggregate,omitempty"`
}

// GetRecommendationDecision returns a persisted recommendation decision by ID.
func (s *Service) GetRecommendationDecision(ctx context.Context, recommendationID string) (domain.RecommendationDecision, *ServiceError) {
	d, found, err := s.decisionStore.Get(ctx, recommendationID)
	if err != nil {
		return domain.RecommendationDecision{}, &ServiceError{Code: "internal", Message: "failed to read recommendation decision"}
	}
	if !found {
		return domain.RecommendationDecision{}, &ServiceError{Code: "not_found", Message: "recommendation decision not found"}
	}
	return d, nil
}

// GetEvidenceBundle returns a compact evidence bundle for a recommendation.
// Includes the decision plus any policy and aggregate data available for the
// same context signature and top-ranked tool.
func (s *Service) GetEvidenceBundle(ctx context.Context, recommendationID string) (EvidenceBundle, *ServiceError) {
	d, svcErr := s.GetRecommendationDecision(ctx, recommendationID)
	if svcErr != nil {
		return EvidenceBundle{}, svcErr
	}
	bundle := EvidenceBundle{Recommendation: d}

	// Enrich with policy if available.
	if s.policyLearned != nil && d.ContextSignature != "" && len(d.Recommendations) > 0 {
		topTool := d.Recommendations[0].ToolID
		if pol, found, err := s.policyLearned.ReadPolicy(ctx, d.ContextSignature, topTool); err == nil && found {
			bundle.Policy = &pol
		}
	}

	// Enrich with aggregate if available.
	if s.telemetryQ != nil && len(d.Recommendations) > 0 {
		topTool := d.Recommendations[0].ToolID
		if stats, found, err := s.telemetryQ.ToolStats(ctx, topTool); err == nil && found {
			bundle.Aggregate = &stats
		}
	}

	return bundle, nil
}

// LearningStatus returns a summary of the learning evidence plane.
type LearningStatus struct {
	Status               string   `json:"status"`
	ActiveAlgorithms     []string `json:"active_algorithms"`
	RecommendationEvents bool     `json:"runtime_recommendation_events"`
	ToolLearningEvents   bool     `json:"tool_learning_events"`
	EvidenceProjection   bool     `json:"evidence_projection_enabled"`
}

// GetLearningStatus returns the current learning pipeline status.
func (s *Service) GetLearningStatus(_ context.Context) LearningStatus {
	algorithms := []string{"heuristic_v1"}
	hasPolicyReader := s.policyLearned != nil
	if hasPolicyReader {
		algorithms = append(algorithms, "thompson_sampling")
	}
	if s.neuralModel != nil {
		algorithms = append(algorithms, "neural_ts")
	}
	return LearningStatus{
		Status:               "active",
		ActiveAlgorithms:     algorithms,
		RecommendationEvents: true,
		ToolLearningEvents:   hasPolicyReader,
		EvidenceProjection:   true,
	}
}

// GetPolicy reads a learned policy for a given context signature + tool ID.
func (s *Service) GetPolicy(ctx context.Context, contextSig, toolID string) (ToolPolicy, *ServiceError) {
	if s.policyLearned == nil {
		return ToolPolicy{}, &ServiceError{Code: "not_found", Message: "policy reader not configured"}
	}
	pol, found, err := s.policyLearned.ReadPolicy(ctx, contextSig, toolID)
	if err != nil {
		return ToolPolicy{}, &ServiceError{Code: "internal", Message: "failed to read policy"}
	}
	if !found {
		return ToolPolicy{}, &ServiceError{Code: "not_found", Message: "policy not found"}
	}
	return pol, nil
}

// ListPolicies returns all policies for a context signature.
func (s *Service) ListPolicies(ctx context.Context, contextSig string) (map[string]ToolPolicy, *ServiceError) {
	if s.policyLearned == nil {
		return nil, &ServiceError{Code: "not_found", Message: "policy reader not configured"}
	}
	policies, err := s.policyLearned.ReadPoliciesForContext(ctx, contextSig)
	if err != nil {
		return nil, &ServiceError{Code: "internal", Message: "failed to list policies"}
	}
	return policies, nil
}

// GetAggregate returns telemetry aggregate stats for a tool.
func (s *Service) GetAggregate(ctx context.Context, toolID string) (ToolStats, *ServiceError) {
	stats, found, err := s.telemetryQ.ToolStats(ctx, toolID)
	if err != nil {
		return ToolStats{}, &ServiceError{Code: "internal", Message: "failed to read aggregate"}
	}
	if !found {
		return ToolStats{}, &ServiceError{Code: "not_found", Message: "aggregate not found"}
	}
	return stats, nil
}

// ─── Agent Feedback Loop ──────────────────────────────────────────────────

// AcceptRecommendation records that an agent used a recommended tool and it solved the task.
func (s *Service) AcceptRecommendation(ctx context.Context, sessionID, recommendationID, selectedToolID string) (string, *ServiceError) {
	session, _, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil {
		return "", &ServiceError{Code: ErrorCodeInternal, Message: "session lookup failed"}
	}

	eventID := newID("evt")
	s.publishEvent(ctx, domain.EventRecommendationAccepted, session, domain.RecommendationAcceptedPayload{
		RecommendationID: recommendationID,
		SelectedToolID:   selectedToolID,
	})

	if s.kpiMetrics != nil {
		s.kpiMetrics.ObserveRecommendationUsed(true)
	}

	return eventID, nil
}

// RejectRecommendation records that an agent skipped a recommendation.
func (s *Service) RejectRecommendation(ctx context.Context, sessionID, recommendationID, reason string) (string, *ServiceError) {
	session, _, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil {
		return "", &ServiceError{Code: ErrorCodeInternal, Message: "session lookup failed"}
	}

	eventID := newID("evt")
	s.publishEvent(ctx, domain.EventRecommendationRejected, session, domain.RecommendationRejectedPayload{
		RecommendationID: recommendationID,
		Reason:           reason,
	})

	if s.kpiMetrics != nil {
		s.kpiMetrics.ObserveRecommendationUsed(false)
	}

	return eventID, nil
}
