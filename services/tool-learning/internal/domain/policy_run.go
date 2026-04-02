package domain

import "time"

// RunStatus represents the lifecycle state of a policy computation run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

// PolicyRun tracks the lifecycle of a single policy computation execution.
// One PolicyRun is created per CronJob invocation (hourly, daily, custom).
type PolicyRun struct {
	RunID            string    `json:"run_id"`
	Schedule         string    `json:"schedule"`
	Status           RunStatus `json:"status"`
	AlgorithmID      string    `json:"algorithm_id"`
	AlgorithmVersion string    `json:"algorithm_version"`
	Window           string    `json:"window"`
	Constraints      []string  `json:"constraints,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	AggregatesRead   int       `json:"aggregates_read"`
	PoliciesWritten  int       `json:"policies_written"`
	PoliciesFiltered int       `json:"policies_filtered"`
	DurationMs       int64     `json:"duration_ms"`
	SnapshotRef      string    `json:"snapshot_ref,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
}
