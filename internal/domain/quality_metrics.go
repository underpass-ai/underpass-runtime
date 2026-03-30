package domain

import (
	"fmt"
	"time"
)

// InvocationQualityMetrics is a domain value object that captures the
// quality dimensions of a tool invocation. All fields are validated at
// construction time — an invalid metric set cannot exist.
//
// This mirrors rehydration-kernel's BundleQualityMetrics pattern:
// metrics are computed in the domain, observed through a hexagonal port,
// and exported by adapter implementations (Prometheus, structured logs, OTel).
type InvocationQualityMetrics struct {
	toolName    string
	status      InvocationStatus
	durationMS  int64
	exitCode    int
	outputBytes int64
	hasError    bool
	errorCode   string
	// Derived quality signals.
	latencyBucket string  // human-readable bucket: "fast", "normal", "slow", "very_slow"
	successRate   float64 // 1.0 for succeeded, 0.0 for failed/denied (per invocation)
}

// ComputeInvocationQuality constructs quality metrics from a completed
// invocation. Returns an error if the invocation is still running.
func ComputeInvocationQuality(inv Invocation) (InvocationQualityMetrics, error) {
	if inv.Status == InvocationStatusRunning {
		return InvocationQualityMetrics{}, fmt.Errorf("cannot compute quality for running invocation %s", inv.ID)
	}

	var outputBytes int64
	if inv.OutputRef != "" {
		// Output was offloaded to artifact store; exact bytes unknown here.
		outputBytes = -1
	}

	var errorCode string
	if inv.Error != nil {
		errorCode = inv.Error.Code
	}

	successRate := 0.0
	if inv.Status == InvocationStatusSucceeded {
		successRate = 1.0
	}

	return InvocationQualityMetrics{
		toolName:      inv.ToolName,
		status:        inv.Status,
		durationMS:    inv.DurationMS,
		exitCode:      inv.ExitCode,
		outputBytes:   outputBytes,
		hasError:      inv.Error != nil,
		errorCode:     errorCode,
		latencyBucket: classifyLatency(inv.DurationMS),
		successRate:   successRate,
	}, nil
}

// Getters — value objects are immutable.

func (m InvocationQualityMetrics) ToolName() string          { return m.toolName }
func (m InvocationQualityMetrics) Status() InvocationStatus  { return m.status }
func (m InvocationQualityMetrics) DurationMS() int64         { return m.durationMS }
func (m InvocationQualityMetrics) ExitCode() int             { return m.exitCode }
func (m InvocationQualityMetrics) OutputBytes() int64        { return m.outputBytes }
func (m InvocationQualityMetrics) HasError() bool            { return m.hasError }
func (m InvocationQualityMetrics) ErrorCode() string         { return m.errorCode }
func (m InvocationQualityMetrics) LatencyBucket() string     { return m.latencyBucket }
func (m InvocationQualityMetrics) SuccessRate() float64      { return m.successRate }

// QualityObservationContext provides metadata for the observer to add as
// labels or structured log fields.
type QualityObservationContext struct {
	SessionID string
	TenantID  string
	ActorID   string
	Timestamp time.Time
}

func classifyLatency(ms int64) string {
	switch {
	case ms <= 100:
		return "fast"
	case ms <= 1000:
		return "normal"
	case ms <= 5000:
		return "slow"
	default:
		return "very_slow"
	}
}
