package domain

import "time"

// ToolPolicy is a computed policy for a (context, tool) pair.
// Written by the learning CronJob, read by the workspace runtime
// for tool ranking at invocation time.
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

// ValkeyKey returns the Valkey key for this policy entry.
// Format: tool_policy:{context_sig}:{tool_id}
func (p ToolPolicy) ValkeyKey(prefix string) string {
	return prefix + ":" + p.ContextSignature + ":" + p.ToolID
}
