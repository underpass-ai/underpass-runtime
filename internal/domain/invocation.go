package domain

import "time"

type InvocationStatus string

const (
	InvocationStatusRunning   InvocationStatus = "running"
	InvocationStatusSucceeded InvocationStatus = "succeeded"
	InvocationStatusFailed    InvocationStatus = "failed"
	InvocationStatusDenied    InvocationStatus = "denied"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type LogLine struct {
	At      time.Time `json:"at"`
	Channel string    `json:"channel"`
	Message string    `json:"message"`
}

type Artifact struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

type Invocation struct {
	ID            string           `json:"id"`
	SessionID     string           `json:"session_id"`
	ToolName      string           `json:"tool_name"`
	CorrelationID string           `json:"correlation_id,omitempty"`
	Status        InvocationStatus `json:"status"`
	StartedAt     time.Time        `json:"started_at"`
	CompletedAt   *time.Time       `json:"completed_at,omitempty"`
	DurationMS    int64            `json:"duration_ms"`
	TraceName     string           `json:"trace_name"`
	SpanName      string           `json:"span_name"`
	ExitCode      int              `json:"exit_code"`
	Output        any              `json:"output,omitempty"`
	OutputRef     string           `json:"output_ref,omitempty"`
	Logs          []LogLine        `json:"logs,omitempty"`
	LogsRef       string           `json:"logs_ref,omitempty"`
	Artifacts     []Artifact       `json:"artifacts,omitempty"`
	Error         *Error           `json:"error,omitempty"`
}
