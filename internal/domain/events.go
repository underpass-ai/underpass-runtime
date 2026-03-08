package domain

import (
	"encoding/json"
	"time"
)

// EventType identifies a workspace domain event.
type EventType string

const (
	EventSessionCreated      EventType = "workspace.session.created"
	EventSessionClosed       EventType = "workspace.session.closed"
	EventInvocationStarted   EventType = "workspace.invocation.started"
	EventInvocationCompleted EventType = "workspace.invocation.completed"
	EventInvocationDenied    EventType = "workspace.invocation.denied"
	EventArtifactStored      EventType = "workspace.artifact.stored"
)

// EventVersion is the schema version for domain events.
const EventVersion = "v1"

// DomainEvent is the envelope for all workspace domain events.
// Payload contains event-type-specific data as JSON.
type DomainEvent struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Version   string          `json:"version"`
	Timestamp time.Time       `json:"timestamp"`
	SessionID string          `json:"session_id"`
	TenantID  string          `json:"tenant_id"`
	ActorID   string          `json:"actor_id"`
	Payload   json.RawMessage `json:"payload"`
}

// --- Typed payloads per event type ---

// SessionCreatedPayload is the payload for EventSessionCreated.
type SessionCreatedPayload struct {
	RuntimeKind  RuntimeKind `json:"runtime_kind"`
	RepoURL      string      `json:"repo_url,omitempty"`
	RepoRef      string      `json:"repo_ref,omitempty"`
	ExpiresAt    time.Time   `json:"expires_at"`
	WorkspaceDir string      `json:"workspace_dir,omitempty"`
}

// SessionClosedPayload is the payload for EventSessionClosed.
type SessionClosedPayload struct {
	RuntimeKind     RuntimeKind `json:"runtime_kind"`
	DurationSec     int64       `json:"duration_sec"`
	InvocationCount int         `json:"invocation_count,omitempty"`
}

// InvocationStartedPayload is the payload for EventInvocationStarted.
type InvocationStartedPayload struct {
	InvocationID  string `json:"invocation_id"`
	ToolName      string `json:"tool_name"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// InvocationCompletedPayload is the payload for EventInvocationCompleted.
type InvocationCompletedPayload struct {
	InvocationID  string           `json:"invocation_id"`
	ToolName      string           `json:"tool_name"`
	CorrelationID string           `json:"correlation_id,omitempty"`
	Status        InvocationStatus `json:"status"`
	ExitCode      int              `json:"exit_code"`
	DurationMS    int64            `json:"duration_ms"`
	OutputBytes   int64            `json:"output_bytes,omitempty"`
	ArtifactCount int              `json:"artifact_count,omitempty"`
	ErrorCode     string           `json:"error_code,omitempty"`
}

// InvocationDeniedPayload is the payload for EventInvocationDenied.
type InvocationDeniedPayload struct {
	InvocationID  string `json:"invocation_id"`
	ToolName      string `json:"tool_name"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Reason        string `json:"reason"`
}

// ArtifactStoredPayload is the payload for EventArtifactStored.
type ArtifactStoredPayload struct {
	InvocationID string `json:"invocation_id"`
	ArtifactID   string `json:"artifact_id"`
	Name         string `json:"name"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
}

// NewDomainEvent constructs a DomainEvent with a typed payload.
// The caller must provide a unique id (e.g. from app.newID).
func NewDomainEvent(id string, eventType EventType, sessionID, tenantID, actorID string, payload any) (DomainEvent, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return DomainEvent{}, err
	}
	return DomainEvent{
		ID:        id,
		Type:      eventType,
		Version:   EventVersion,
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		TenantID:  tenantID,
		ActorID:   actorID,
		Payload:   raw,
	}, nil
}
