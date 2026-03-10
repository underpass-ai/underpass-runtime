package domain

import "time"

// ToolInvocation is a fact record in the telemetry lake (Parquet).
// Maps 1:1 to a row in the tool_invocations analytical table.
type ToolInvocation struct {
	InvocationID     string    `json:"invocation_id"     parquet:"invocation_id"`
	Dt               string    `json:"dt"                parquet:"dt"`
	Ts               time.Time `json:"ts"                parquet:"ts"`
	ToolID           string    `json:"tool_id"           parquet:"tool_id"`
	AgentIDHash      string    `json:"agent_id_hash"     parquet:"agent_id_hash"`
	TaskID           string    `json:"task_id"            parquet:"task_id"`
	ContextSignature string    `json:"context_signature"  parquet:"context_signature"`
	Outcome          string    `json:"outcome"            parquet:"outcome"`
	ErrorType        string    `json:"error_type"         parquet:"error_type"`
	LatencyMs        int64     `json:"latency_ms"         parquet:"latency_ms"`
	CostUnits        float64   `json:"cost_units"         parquet:"cost_units"`
	ToolVersion      string    `json:"tool_version"       parquet:"tool_version"`
}

// Outcome constants.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// IsSuccess returns true if the invocation succeeded.
func (t ToolInvocation) IsSuccess() bool {
	return t.Outcome == OutcomeSuccess
}
