package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// capturingEventPublisher records all published events for assertion.
type capturingEventPublisher struct {
	mu     sync.Mutex
	events []domain.DomainEvent
}

func (p *capturingEventPublisher) Publish(_ context.Context, event domain.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
	return nil
}

func (p *capturingEventPublisher) Events() []domain.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domain.DomainEvent, len(p.events))
	copy(out, p.events)
	return out
}

func (p *capturingEventPublisher) EventsOfType(t domain.EventType) []domain.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []domain.DomainEvent
	for _, e := range p.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// --- Use Case: Session Lifecycle (create → invoke → close) with events ---

func TestIntegration_SessionLifecycle_EventsPublished(t *testing.T) {
	svc := setupService(t)
	pub := &capturingEventPublisher{}
	svc.SetEventPublisher(pub)
	ctx := context.Background()

	// 1. Create session → EventSessionCreated
	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	created := pub.EventsOfType(domain.EventSessionCreated)
	if len(created) != 1 {
		t.Fatalf("expected 1 session.created event, got %d", len(created))
	}
	if created[0].SessionID != session.ID {
		t.Fatalf("event session_id mismatch: %s vs %s", created[0].SessionID, session.ID)
	}
	if created[0].TenantID != testTenantID {
		t.Fatalf("event tenant_id mismatch: %s", created[0].TenantID)
	}

	var createdPayload domain.SessionCreatedPayload
	if unmarshalErr := json.Unmarshal(created[0].Payload, &createdPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal created payload: %v", unmarshalErr)
	}
	if createdPayload.WorkspaceDir == "" {
		t.Fatal("expected workspace_dir in created payload")
	}

	// 2. Invoke a tool → EventInvocationStarted + EventInvocationCompleted
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if invokeErr != nil {
		t.Fatalf("invoke fs.list: %v", invokeErr)
	}

	started := pub.EventsOfType(domain.EventInvocationStarted)
	if len(started) != 1 {
		t.Fatalf("expected 1 invocation.started event, got %d", len(started))
	}
	var startedPayload domain.InvocationStartedPayload
	if unmarshalErr := json.Unmarshal(started[0].Payload, &startedPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal started payload: %v", unmarshalErr)
	}
	if startedPayload.ToolName != "fs.list" {
		t.Fatalf("expected tool_name=fs.list, got %s", startedPayload.ToolName)
	}

	completed := pub.EventsOfType(domain.EventInvocationCompleted)
	if len(completed) != 1 {
		t.Fatalf("expected 1 invocation.completed event, got %d", len(completed))
	}
	var completedPayload domain.InvocationCompletedPayload
	if unmarshalErr := json.Unmarshal(completed[0].Payload, &completedPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal completed payload: %v", unmarshalErr)
	}
	if completedPayload.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("expected succeeded status in event, got %s", completedPayload.Status)
	}

	// 3. Close session → EventSessionClosed
	if closeErr := svc.CloseSession(ctx, session.ID); closeErr != nil {
		t.Fatalf("close session: %v", closeErr)
	}

	closed := pub.EventsOfType(domain.EventSessionClosed)
	if len(closed) != 1 {
		t.Fatalf("expected 1 session.closed event, got %d", len(closed))
	}
	if closed[0].SessionID != session.ID {
		t.Fatalf("closed event session_id mismatch: %s", closed[0].SessionID)
	}

	var closedPayload domain.SessionClosedPayload
	if unmarshalErr := json.Unmarshal(closed[0].Payload, &closedPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal closed payload: %v", unmarshalErr)
	}
	if closedPayload.DurationSec < 0 {
		t.Fatalf("expected non-negative duration, got %d", closedPayload.DurationSec)
	}
}

// --- Use Case: Invocation Denied → event published with reason ---

func TestIntegration_InvocationDenied_EventPublished(t *testing.T) {
	svc := setupService(t)
	pub := &capturingEventPublisher{}
	svc.SetEventPublisher(pub)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// fs.write_file requires approval; invoke without approval → denied
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "test.txt", "content": "x"}),
	})
	if invokeErr == nil {
		t.Fatal("expected approval error")
	}
	if invokeErr.Code != app.ErrorCodeApprovalRequired {
		t.Fatalf("expected approval_required, got %s", invokeErr.Code)
	}

	denied := pub.EventsOfType(domain.EventInvocationDenied)
	if len(denied) != 1 {
		t.Fatalf("expected 1 invocation.denied event, got %d", len(denied))
	}

	var deniedPayload domain.InvocationDeniedPayload
	if unmarshalErr := json.Unmarshal(denied[0].Payload, &deniedPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal denied payload: %v", unmarshalErr)
	}
	if deniedPayload.ToolName != "fs.write_file" {
		t.Fatalf("expected tool_name=fs.write_file, got %s", deniedPayload.ToolName)
	}
	if deniedPayload.Reason == "" {
		t.Fatal("expected non-empty denial reason")
	}
}

// --- Use Case: Multi-Tenant Session Isolation ---

func TestIntegration_MultiTenantIsolation(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	// Tenant A creates a session
	sessionA, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: "tenant-a", ActorID: "alice", Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}

	// Tenant B creates a session
	sessionB, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: "tenant-b", ActorID: "bob", Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session B: %v", err)
	}

	// Tenant A can access their own session
	if accessErr := svc.ValidateSessionAccess(ctx, sessionA.ID, sessionA.Principal); accessErr != nil {
		t.Fatalf("tenant A self-access denied: %v", accessErr)
	}

	// Tenant B cannot access Tenant A's session
	crossTenantErr := svc.ValidateSessionAccess(ctx, sessionA.ID, sessionB.Principal)
	if crossTenantErr == nil {
		t.Fatal("expected cross-tenant access to be denied")
	}
	if crossTenantErr.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy_denied, got %s", crossTenantErr.Code)
	}

	// Same tenant, different actor cannot access
	sameTenanDiffActor := domain.Principal{TenantID: "tenant-a", ActorID: "eve", Roles: []string{testRoleDeveloper}}
	diffActorErr := svc.ValidateSessionAccess(ctx, sessionA.ID, sameTenanDiffActor)
	if diffActorErr == nil {
		t.Fatal("expected different-actor access to be denied")
	}

	// Tenant A invokes tool in session A, then tenant B cannot access the invocation
	invocation, invokeErr := svc.InvokeTool(ctx, sessionA.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if invokeErr != nil {
		t.Fatalf("invoke in session A: %v", invokeErr)
	}

	invAccessErr := svc.ValidateInvocationAccess(ctx, invocation.ID, sessionB.Principal)
	if invAccessErr == nil {
		t.Fatal("expected cross-tenant invocation access to be denied")
	}
}

// --- Use Case: Correlation ID Deduplication (end-to-end with real adapters) ---

func TestIntegration_CorrelationIDDeduplication(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	corrID := "dedup-test-001"

	// First invocation with correlation ID
	first, firstErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		CorrelationID: corrID,
		Args:          mustJSON(t, map[string]any{"path": "."}),
	})
	if firstErr != nil {
		t.Fatalf("first invoke: %v", firstErr)
	}
	if first.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", first.Status)
	}

	// Second invocation with same correlation ID → should return same invocation
	second, secondErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		CorrelationID: corrID,
		Args:          mustJSON(t, map[string]any{"path": "."}),
	})
	if secondErr != nil {
		t.Fatalf("second invoke: %v", secondErr)
	}

	if first.ID != second.ID {
		t.Fatalf("expected same invocation ID for dedup, got %s vs %s", first.ID, second.ID)
	}

	// Different correlation ID → new invocation
	third, thirdErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		CorrelationID: "dedup-test-002",
		Args:          mustJSON(t, map[string]any{"path": "."}),
	})
	if thirdErr != nil {
		t.Fatalf("third invoke: %v", thirdErr)
	}
	if third.ID == first.ID {
		t.Fatal("expected new invocation for different correlation ID")
	}
}

// --- Use Case: Write → Read → Get Invocation → Logs → Artifacts (full data flow) ---

func TestIntegration_InvocationDataFlow(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Write a file
	writeInv, writeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Approved: true,
		Args:     mustJSON(t, map[string]any{"path": "data/test.txt", "content": "integration test", "create_parents": true}),
	})
	if writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	if writeInv.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("write status: %s", writeInv.Status)
	}

	// Read the file back
	readInv, readErr := svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "data/test.txt"}),
	})
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	output, ok := readInv.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", readInv.Output)
	}
	if output["content"] != "integration test" {
		t.Fatalf("content mismatch: %v", output["content"])
	}

	// Get invocation by ID → should hydrate output
	retrieved, getErr := svc.GetInvocation(ctx, readInv.ID)
	if getErr != nil {
		t.Fatalf("get invocation: %v", getErr)
	}
	if retrieved.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("retrieved status: %s", retrieved.Status)
	}
	if retrieved.Output == nil {
		t.Fatal("expected hydrated output on retrieved invocation")
	}

	// Get logs
	logs, logsErr := svc.GetInvocationLogs(ctx, readInv.ID)
	if logsErr != nil {
		t.Fatalf("get logs: %v", logsErr)
	}
	// fs.read_file may or may not produce logs, just verify no error
	_ = logs

	// Get artifacts
	artifacts, artErr := svc.GetInvocationArtifacts(ctx, readInv.ID)
	if artErr != nil {
		t.Fatalf("get artifacts: %v", artErr)
	}
	if len(artifacts) == 0 {
		t.Fatal("expected at least one artifact (output)")
	}

	// Verify artifact names include the output artifact
	hasOutput := false
	for _, a := range artifacts {
		if strings.Contains(a.Name, "output") {
			hasOutput = true
		}
	}
	if !hasOutput {
		t.Fatal("expected output artifact")
	}
}

// --- Use Case: Metrics updated after invocations ---

func TestIntegration_PrometheusMetrics_AfterInvocations(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Initial metrics should exist
	metrics := svc.PrometheusMetrics()
	if metrics == "" {
		t.Fatal("expected non-empty initial metrics")
	}

	// Invoke a tool
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if invokeErr != nil {
		t.Fatalf("invoke: %v", invokeErr)
	}

	// Metrics should now include the tool invocation counter
	updated := svc.PrometheusMetrics()
	if !strings.Contains(updated, `invocations_total{tool="fs.list",status="succeeded"} 1`) {
		t.Fatalf("expected fs.list succeeded counter, got:\n%s", updated)
	}
}
