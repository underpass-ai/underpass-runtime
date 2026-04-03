package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
)

type fakeRuntimeClient struct {
	sessionID string
	recTools  []string
	recID     string
	invokeRes InvokeResult
	invokeErr error
	acceptErr error
	rejectErr error
	accepted  bool
	rejected  bool
}

func (f *fakeRuntimeClient) CreateSession(_ context.Context, _, _ string) (string, error) {
	return f.sessionID, nil
}
func (f *fakeRuntimeClient) CloseSession(_ context.Context, _ string) error { return nil }
func (f *fakeRuntimeClient) RecommendTools(_ context.Context, _, _ string, _ int) ([]string, string, error) {
	return f.recTools, f.recID, nil
}
func (f *fakeRuntimeClient) InvokeTool(_ context.Context, _, _ string) (InvokeResult, error) {
	return f.invokeRes, f.invokeErr
}
func (f *fakeRuntimeClient) AcceptRecommendation(_ context.Context, _, _, _ string) error {
	f.accepted = true
	return f.acceptErr
}
func (f *fakeRuntimeClient) RejectRecommendation(_ context.Context, _, _, _ string) error {
	f.rejected = true
	return f.rejectErr
}

func firingAlert(name string) []byte {
	data, _ := json.Marshal(AlertEvent{
		ID: "alert-1", Type: "observability.alert.fired", AlertName: name,
		Status: "firing", Severity: "warning", Summary: "test alert",
	})
	return data
}

func TestAgent_HandleAlert_Success(t *testing.T) {
	client := &fakeRuntimeClient{
		sessionID: "sess-rem-1",
		recTools:  []string{"fs.list", "fs.read_file"},
		recID:     "rec-1",
		invokeRes: InvokeResult{Status: "INVOCATION_STATUS_SUCCEEDED", DurationMS: 50},
	}
	agent := NewAgent(AgentConfig{
		Client: client, TenantID: "t1", ActorID: "remediation-agent",
		Logger: slog.Default(),
	})

	result, err := agent.HandleAlert(context.Background(), firingAlert("WorkspaceInvocationFailureRateHigh"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Outcome != "success" {
		t.Fatalf("expected success, got %s", result.Outcome)
	}
	if result.SessionID != "sess-rem-1" {
		t.Fatalf("expected session ID, got %s", result.SessionID)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(result.Steps))
	}
	if !client.accepted {
		t.Fatal("expected recommendation accepted")
	}
}

func TestAgent_HandleAlert_PartialFailure(t *testing.T) {
	callCount := 0
	client := &fakeRuntimeClient{
		sessionID: "sess-rem-2",
		recTools:  []string{"fs.list"},
		recID:     "rec-2",
		invokeRes: InvokeResult{Status: "INVOCATION_STATUS_FAILED", DurationMS: 100},
	}
	_ = callCount // tools will "fail"
	agent := NewAgent(AgentConfig{
		Client: client, TenantID: "t1", ActorID: "remediation-agent",
		Logger: slog.Default(),
	})

	result, err := agent.HandleAlert(context.Background(), firingAlert("WorkspaceInvocationFailureRateHigh"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "partial" {
		t.Fatalf("expected partial, got %s", result.Outcome)
	}
	if !client.rejected {
		t.Fatal("expected recommendation rejected on partial failure")
	}
}

func TestAgent_HandleAlert_NoPlaybook(t *testing.T) {
	agent := NewAgent(AgentConfig{
		Client: &fakeRuntimeClient{}, TenantID: "t1", ActorID: "agent",
		Logger: slog.Default(),
	})

	result, err := agent.HandleAlert(context.Background(), firingAlert("UnknownAlert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for unknown alert")
	}
}

func TestAgent_HandleAlert_AcceptFeedbackError(t *testing.T) {
	client := &fakeRuntimeClient{
		sessionID: "sess-af", recTools: []string{"fs.list"}, recID: "rec-af",
		invokeRes: InvokeResult{Status: "INVOCATION_STATUS_SUCCEEDED"},
		acceptErr: fmt.Errorf("nats down"),
	}
	agent := NewAgent(AgentConfig{Client: client, TenantID: "t1", ActorID: "agent", Logger: slog.Default()})
	result, err := agent.HandleAlert(context.Background(), firingAlert("WorkspaceInvocationFailureRateHigh"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "success" {
		t.Fatalf("expected success despite feedback error, got %s", result.Outcome)
	}
}

func TestAgent_HandleAlert_RejectFeedbackError(t *testing.T) {
	client := &fakeRuntimeClient{
		sessionID: "sess-rf", recTools: []string{"fs.list"}, recID: "rec-rf",
		invokeRes: InvokeResult{Status: "INVOCATION_STATUS_FAILED"},
		rejectErr: fmt.Errorf("nats down"),
	}
	agent := NewAgent(AgentConfig{Client: client, TenantID: "t1", ActorID: "agent", Logger: slog.Default()})
	result, err := agent.HandleAlert(context.Background(), firingAlert("WorkspaceInvocationFailureRateHigh"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "partial" {
		t.Fatalf("expected partial despite feedback error, got %s", result.Outcome)
	}
}

func TestAgent_HandleAlert_ResolvedSkipped(t *testing.T) {
	data, _ := json.Marshal(AlertEvent{
		AlertName: "WorkspaceDown", Status: "resolved",
	})
	agent := NewAgent(AgentConfig{
		Client: &fakeRuntimeClient{}, TenantID: "t1", ActorID: "agent",
		Logger: slog.Default(),
	})

	result, err := agent.HandleAlert(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for resolved alert")
	}
}

func TestAgent_HandleAlert_InvalidJSON(t *testing.T) {
	agent := NewAgent(AgentConfig{
		Client: &fakeRuntimeClient{}, TenantID: "t1", ActorID: "agent",
		Logger: slog.Default(),
	})
	_, err := agent.HandleAlert(context.Background(), []byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMatchPlaybook(t *testing.T) {
	playbooks := DefaultPlaybooks()

	pb, found := MatchPlaybook(playbooks, "WorkspaceDown")
	if !found {
		t.Fatal("expected to find WorkspaceDown playbook")
	}
	if pb.Severity != "critical" {
		t.Fatalf("expected critical severity, got %s", pb.Severity)
	}

	_, found = MatchPlaybook(playbooks, "NonExistent")
	if found {
		t.Fatal("expected not found for unknown alert")
	}
}

func TestDefaultPlaybooks(t *testing.T) {
	playbooks := DefaultPlaybooks()
	if len(playbooks) < 4 {
		t.Fatalf("expected at least 4 playbooks, got %d", len(playbooks))
	}
}
