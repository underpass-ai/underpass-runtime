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

// PolicyEventPublisher publishes lifecycle and policy events.
type PolicyEventPublisher interface {
	// PublishPolicyUpdated notifies downstream consumers that policies have been recomputed.
	PublishPolicyUpdated(ctx context.Context, policies []domain.ToolPolicy, filtered int) error
	// PublishRunStarted emits tool_learning.run.started at pipeline start.
	PublishRunStarted(ctx context.Context, run domain.PolicyRun) error
	// PublishRunCompleted emits tool_learning.run.completed after successful pipeline.
	PublishRunCompleted(ctx context.Context, run domain.PolicyRun) error
	// PublishRunFailed emits tool_learning.run.failed on pipeline error.
	PublishRunFailed(ctx context.Context, run domain.PolicyRun) error
	// PublishPolicyComputed emits tool_learning.policy.computed per policy batch.
	PublishPolicyComputed(ctx context.Context, run domain.PolicyRun, policies []domain.ToolPolicy) error
	// PublishSnapshotPublished emits tool_learning.snapshot.published after audit write.
	PublishSnapshotPublished(ctx context.Context, run domain.PolicyRun, snapshotRef string) error
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

// NeuralModelStore writes trained neural model weights to Valkey.
type NeuralModelStore interface {
	WriteNeuralModel(ctx context.Context, key string, data []byte) error
}

// PolicyComputer computes tool policies from aggregate telemetry.
// Implementations: domain.ThompsonSampler, domain.ThompsonSamplerLLM.
type PolicyComputer interface {
	ComputePolicy(contextSig, toolID string, stats domain.AggregateStats) domain.ToolPolicy
}
