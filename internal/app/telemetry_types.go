package app

import (
	"context"
	"time"
)

// TelemetryRecord is a derived feature vector per invocation, stored for
// offline analysis and online ranking. It captures only computed metrics,
// not raw logs or output.
type TelemetryRecord struct {
	InvocationID  string    `json:"invocation_id"`
	SessionID     string    `json:"session_id"`
	ToolName      string    `json:"tool_name"`
	ToolFamily    string    `json:"tool_family"`
	ToolsetID     string    `json:"toolset_id"`
	RuntimeKind   string    `json:"runtime_kind"`
	RepoLanguage  string    `json:"repo_language"`
	ProjectType   string    `json:"project_type"`
	TenantID      string    `json:"tenant_id"`
	Approved      bool      `json:"approved"`
	Status        string    `json:"status"`
	ErrorCode     string    `json:"error_code,omitempty"`
	DurationMs    int64     `json:"duration_ms"`
	OutputBytes   int64     `json:"output_bytes"`
	LogsBytes     int64     `json:"logs_bytes"`
	ArtifactCount int       `json:"artifact_count"`
	ArtifactBytes int64     `json:"artifact_bytes"`
	ContextSig    string    `json:"context_sig,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// ToolPolicy represents a learned policy from the tool-learning pipeline.
// Mirror of services/tool-learning/internal/domain.ToolPolicy to avoid import cycle.
type ToolPolicy struct {
	ContextSignature string    `json:"context_signature"`
	ToolID           string    `json:"tool_id"`
	Alpha            float64   `json:"alpha"`
	Beta             float64   `json:"beta"`
	P95LatencyMs     int64     `json:"p95_latency_ms"`
	P95Cost          float64   `json:"p95_cost"`
	ErrorRate        float64   `json:"error_rate"`
	NSamples         int64     `json:"n_samples"`
	FreshnessTs      time.Time `json:"freshness_ts"`
	Confidence       float64   `json:"confidence"`
}

// TelemetryRecorder persists telemetry records for invocations. Implementations
// must be safe for concurrent use.
type TelemetryRecorder interface {
	Record(ctx context.Context, record TelemetryRecord) error
}

// TelemetryQuerier reads aggregated telemetry stats. Used by the discovery
// endpoint and recommendation engine.
type TelemetryQuerier interface {
	ToolStats(ctx context.Context, toolName string) (ToolStats, bool, error)
	AllToolStats(ctx context.Context) (map[string]ToolStats, error)
}
