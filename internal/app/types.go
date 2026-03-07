package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type CreateSessionRequest struct {
	SessionID       string            `json:"session_id"`
	RepoURL         string            `json:"repo_url,omitempty"`
	RepoRef         string            `json:"repo_ref,omitempty"`
	SourceRepoPath  string            `json:"source_repo_path,omitempty"`
	AllowedPaths    []string          `json:"allowed_paths,omitempty"`
	Principal       domain.Principal  `json:"principal"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ExpiresInSecond int               `json:"expires_in_seconds,omitempty"`
}

type InvokeToolRequest struct {
	CorrelationID string          `json:"correlation_id,omitempty"`
	Args          json.RawMessage `json:"args"`
	Approved      bool            `json:"approved"`
}

type ToolRunResult struct {
	Output    any
	Logs      []domain.LogLine
	Artifacts []ArtifactPayload
	ExitCode  int
}

type CommandSpec struct {
	Cwd      string
	Command  string
	Args     []string
	Stdin    []byte
	MaxBytes int
}

type CommandResult struct {
	Output   string
	ExitCode int
}

type ArtifactPayload struct {
	Name        string
	ContentType string
	Data        []byte
}

type WorkspaceManager interface {
	CreateSession(ctx context.Context, req CreateSessionRequest) (domain.Session, error)
	GetSession(ctx context.Context, sessionID string) (domain.Session, bool, error)
	CloseSession(ctx context.Context, sessionID string) error
}

type SessionStore interface {
	Save(ctx context.Context, session domain.Session) error
	Get(ctx context.Context, sessionID string) (domain.Session, bool, error)
	Delete(ctx context.Context, sessionID string) error
}

type CapabilityRegistry interface {
	Get(name string) (domain.Capability, bool)
	List() []domain.Capability
}

type PolicyInput struct {
	Session    domain.Session
	Capability domain.Capability
	Args       json.RawMessage
	Approved   bool
}

type PolicyDecision struct {
	Allow        bool
	ErrorCode    string
	Reason       string
	RequiresAuth bool
}

type Authorizer interface {
	Authorize(ctx context.Context, input PolicyInput) (PolicyDecision, error)
}

type Invoker interface {
	Invoke(ctx context.Context, session domain.Session, capability domain.Capability, args json.RawMessage) (ToolRunResult, *domain.Error)
}

type CommandRunner interface {
	Run(ctx context.Context, session domain.Session, spec CommandSpec) (CommandResult, error)
}

type InvocationStore interface {
	Save(ctx context.Context, invocation domain.Invocation) error
	Get(ctx context.Context, invocationID string) (domain.Invocation, bool, error)
}

type CorrelationFinder interface {
	FindByCorrelation(
		ctx context.Context,
		sessionID string,
		toolName string,
		correlationID string,
	) (domain.Invocation, bool, error)
}

type ArtifactStore interface {
	Save(ctx context.Context, invocationID string, payloads []ArtifactPayload) ([]domain.Artifact, error)
	List(ctx context.Context, invocationID string) ([]domain.Artifact, error)
	Read(ctx context.Context, path string) ([]byte, error)
}

type AuditEvent struct {
	At            time.Time         `json:"at"`
	SessionID     string            `json:"session_id"`
	ToolName      string            `json:"tool_name"`
	InvocationID  string            `json:"invocation_id"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Status        string            `json:"status"`
	ActorID       string            `json:"actor_id"`
	TenantID      string            `json:"tenant_id"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type AuditLogger interface {
	Record(ctx context.Context, event AuditEvent)
}
