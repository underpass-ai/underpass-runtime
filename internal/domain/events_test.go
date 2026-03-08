package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventTypeConstants(t *testing.T) {
	types := []EventType{
		EventSessionCreated,
		EventSessionClosed,
		EventInvocationStarted,
		EventInvocationCompleted,
		EventInvocationDenied,
		EventArtifactStored,
	}

	seen := make(map[EventType]bool, len(types))
	for _, et := range types {
		if et == "" {
			t.Fatal("event type must not be empty")
		}
		if seen[et] {
			t.Fatalf("duplicate event type: %s", et)
		}
		seen[et] = true
	}

	if len(types) != 6 {
		t.Fatalf("expected 6 event types, got %d", len(types))
	}
}

func TestEventVersion(t *testing.T) {
	if EventVersion != "v1" {
		t.Fatalf("expected version v1, got %s", EventVersion)
	}
}

func TestNewDomainEvent_SessionCreated(t *testing.T) {
	payload := SessionCreatedPayload{
		RuntimeKind:  RuntimeKindDocker,
		RepoURL:      "https://github.com/org/repo",
		RepoRef:      "main",
		ExpiresAt:    time.Now().Add(time.Hour),
		WorkspaceDir: "/workspace/repo",
	}

	evt, err := NewDomainEvent("evt-001", EventSessionCreated, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if evt.ID != "evt-001" {
		t.Fatalf("expected id evt-001, got %s", evt.ID)
	}
	if evt.Type != EventSessionCreated {
		t.Fatalf("expected type %s, got %s", EventSessionCreated, evt.Type)
	}
	if evt.Version != EventVersion {
		t.Fatalf("expected version %s, got %s", EventVersion, evt.Version)
	}
	if evt.SessionID != "sess-1" {
		t.Fatalf("expected session_id sess-1, got %s", evt.SessionID)
	}
	if evt.TenantID != "tenant-a" {
		t.Fatalf("expected tenant_id tenant-a, got %s", evt.TenantID)
	}
	if evt.ActorID != "actor-x" {
		t.Fatalf("expected actor_id actor-x, got %s", evt.ActorID)
	}
	if evt.Timestamp.IsZero() {
		t.Fatal("timestamp should not be zero")
	}

	var decoded SessionCreatedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.RuntimeKind != RuntimeKindDocker {
		t.Fatalf("expected runtime_kind docker, got %s", decoded.RuntimeKind)
	}
	if decoded.RepoURL != "https://github.com/org/repo" {
		t.Fatalf("expected repo_url, got %s", decoded.RepoURL)
	}
}

func TestNewDomainEvent_SessionClosed(t *testing.T) {
	payload := SessionClosedPayload{
		RuntimeKind:     RuntimeKindLocal,
		DurationSec:     3600,
		InvocationCount: 42,
	}

	evt, err := NewDomainEvent("evt-002", EventSessionClosed, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if evt.Type != EventSessionClosed {
		t.Fatalf("expected type %s, got %s", EventSessionClosed, evt.Type)
	}

	var decoded SessionClosedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.DurationSec != 3600 {
		t.Fatalf("expected duration_sec 3600, got %d", decoded.DurationSec)
	}
	if decoded.InvocationCount != 42 {
		t.Fatalf("expected invocation_count 42, got %d", decoded.InvocationCount)
	}
}

func TestNewDomainEvent_InvocationStarted(t *testing.T) {
	payload := InvocationStartedPayload{
		InvocationID:  "inv-abc",
		ToolName:      "fs.read",
		CorrelationID: "corr-1",
	}

	evt, err := NewDomainEvent("evt-003", EventInvocationStarted, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded InvocationStartedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.ToolName != "fs.read" {
		t.Fatalf("expected tool_name fs.read, got %s", decoded.ToolName)
	}
	if decoded.CorrelationID != "corr-1" {
		t.Fatalf("expected correlation_id corr-1, got %s", decoded.CorrelationID)
	}
}

func TestNewDomainEvent_InvocationCompleted(t *testing.T) {
	payload := InvocationCompletedPayload{
		InvocationID:  "inv-abc",
		ToolName:      "repo.test",
		Status:        InvocationStatusSucceeded,
		ExitCode:      0,
		DurationMS:    1500,
		OutputBytes:   4096,
		ArtifactCount: 2,
	}

	evt, err := NewDomainEvent("evt-004", EventInvocationCompleted, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded InvocationCompletedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.Status != InvocationStatusSucceeded {
		t.Fatalf("expected status succeeded, got %s", decoded.Status)
	}
	if decoded.DurationMS != 1500 {
		t.Fatalf("expected duration_ms 1500, got %d", decoded.DurationMS)
	}
	if decoded.ArtifactCount != 2 {
		t.Fatalf("expected artifact_count 2, got %d", decoded.ArtifactCount)
	}
}

func TestNewDomainEvent_InvocationDenied(t *testing.T) {
	payload := InvocationDeniedPayload{
		InvocationID: "inv-deny",
		ToolName:     "fs.write",
		Reason:       "path not in allowed list",
	}

	evt, err := NewDomainEvent("evt-005", EventInvocationDenied, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded InvocationDeniedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.Reason != "path not in allowed list" {
		t.Fatalf("expected denial reason, got %s", decoded.Reason)
	}
}

func TestNewDomainEvent_ArtifactStored(t *testing.T) {
	payload := ArtifactStoredPayload{
		InvocationID: "inv-abc",
		ArtifactID:   "art-001",
		Name:         "coverage.out",
		ContentType:  "text/plain",
		SizeBytes:    8192,
		SHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	evt, err := NewDomainEvent("evt-006", EventArtifactStored, "sess-1", "tenant-a", "actor-x", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded ArtifactStoredPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.SizeBytes != 8192 {
		t.Fatalf("expected size_bytes 8192, got %d", decoded.SizeBytes)
	}
	if decoded.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("expected sha256 hash, got %s", decoded.SHA256)
	}
}

func TestNewDomainEvent_InvalidPayload(t *testing.T) {
	// channels cannot be marshaled to JSON
	_, err := NewDomainEvent("evt-bad", EventSessionCreated, "sess-1", "t", "a", make(chan int))
	if err == nil {
		t.Fatal("expected marshal error for unmarshalable payload")
	}
}

func TestDomainEvent_JSONRoundTrip(t *testing.T) {
	payload := InvocationCompletedPayload{
		InvocationID: "inv-rt",
		ToolName:     "git.status",
		Status:       InvocationStatusFailed,
		ExitCode:     1,
		DurationMS:   250,
		ErrorCode:    "execution_failed",
	}

	original, err := NewDomainEvent("evt-rt", EventInvocationCompleted, "sess-rt", "t1", "a1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored DomainEvent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.ID != original.ID {
		t.Fatalf("id mismatch: %s vs %s", restored.ID, original.ID)
	}
	if restored.Type != original.Type {
		t.Fatalf("type mismatch: %s vs %s", restored.Type, original.Type)
	}
	if restored.Version != original.Version {
		t.Fatalf("version mismatch: %s vs %s", restored.Version, original.Version)
	}
	if restored.SessionID != original.SessionID {
		t.Fatalf("session_id mismatch: %s vs %s", restored.SessionID, original.SessionID)
	}
	if restored.TenantID != original.TenantID {
		t.Fatalf("tenant_id mismatch: %s vs %s", restored.TenantID, original.TenantID)
	}

	var decoded InvocationCompletedPayload
	if err := json.Unmarshal(restored.Payload, &decoded); err != nil {
		t.Fatalf("payload unmarshal error: %v", err)
	}
	if decoded.ErrorCode != "execution_failed" {
		t.Fatalf("expected error_code execution_failed, got %s", decoded.ErrorCode)
	}
}

func TestNewDomainEvent_TimestampIsUTC(t *testing.T) {
	evt, err := NewDomainEvent("evt-tz", EventSessionCreated, "s", "t", "a", SessionCreatedPayload{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Timestamp.Location() != time.UTC {
		t.Fatalf("expected UTC timestamp, got %s", evt.Timestamp.Location())
	}
}

func TestNewDomainEvent_CompletedWithFailedStatus(t *testing.T) {
	payload := InvocationCompletedPayload{
		InvocationID: "inv-fail",
		ToolName:     "repo.build",
		Status:       InvocationStatusFailed,
		ExitCode:     2,
		DurationMS:   5000,
		ErrorCode:    "build_failed",
	}

	evt, err := NewDomainEvent("evt-fail", EventInvocationCompleted, "sess-f", "t1", "a1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded InvocationCompletedPayload
	if err := json.Unmarshal(evt.Payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.Status != InvocationStatusFailed {
		t.Fatalf("expected failed status, got %s", decoded.Status)
	}
	if decoded.ExitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", decoded.ExitCode)
	}
}
