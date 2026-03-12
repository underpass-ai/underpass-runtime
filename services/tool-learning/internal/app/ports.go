package app

import (
	"context"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// TelemetryLakeReader reads aggregated invocation stats from the Parquet lake.
type TelemetryLakeReader interface {
	// QueryAggregates scans invocations in [from, to) and returns per-(context, tool) aggregates.
	QueryAggregates(ctx context.Context, from, to time.Time) ([]domain.AggregateStats, error)
}

// PolicyStore writes and reads computed tool policies.
type PolicyStore interface {
	// WritePolicy persists a single computed policy.
	WritePolicy(ctx context.Context, policy domain.ToolPolicy) error
	// WritePolicies persists a batch of policies atomically.
	WritePolicies(ctx context.Context, policies []domain.ToolPolicy) error
	// ReadPolicy reads a single policy by context signature and tool ID.
	ReadPolicy(ctx context.Context, contextSig, toolID string) (domain.ToolPolicy, bool, error)
}

// PolicyEventPublisher publishes policy update events.
type PolicyEventPublisher interface {
	// PublishPolicyUpdated notifies downstream consumers that policies have been recomputed.
	PublishPolicyUpdated(ctx context.Context, policies []domain.ToolPolicy, filtered int) error
}

// SnapshotWriter writes policy snapshots for audit trail.
type SnapshotWriter interface {
	// WriteSnapshot persists a timestamped batch of policies for audit.
	WriteSnapshot(ctx context.Context, ts time.Time, policies []domain.ToolPolicy) error
}

// PolicyAuditStore is an alias retained for readability in adapter wiring.
type PolicyAuditStore = SnapshotWriter

// Nower abstracts time for testability.
type Nower interface {
	Now() time.Time
}

// Clock is an alias retained for readability.
type Clock = Nower

// RealClock returns the actual system time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
