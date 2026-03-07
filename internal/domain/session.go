package domain

import "time"

type Principal struct {
	TenantID string   `json:"tenant_id"`
	ActorID  string   `json:"actor_id"`
	Roles    []string `json:"roles"`
}

type Session struct {
	ID            string            `json:"id"`
	WorkspacePath string            `json:"workspace_path"`
	Runtime       RuntimeRef        `json:"runtime,omitempty"`
	RepoURL       string            `json:"repo_url,omitempty"`
	RepoRef       string            `json:"repo_ref,omitempty"`
	AllowedPaths  []string          `json:"allowed_paths"`
	Principal     Principal         `json:"principal"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
}

type RuntimeKind string

const (
	RuntimeKindLocal      RuntimeKind = "local"
	RuntimeKindKubernetes RuntimeKind = "kubernetes"
)

type RuntimeRef struct {
	Kind      RuntimeKind `json:"kind,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	PodName   string      `json:"pod_name,omitempty"`
	Container string      `json:"container,omitempty"`
	Workdir   string      `json:"workdir,omitempty"`
}
