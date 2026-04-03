package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

const (
	AlgorithmIDThompson = "beta_thompson_sampling"
	AlgorithmVersionV1  = "1.0.0"
)

// ComputePolicyUseCase reads telemetry from the lake, computes tool
// policies via a pluggable PolicyComputer, persists them, and publishes
// update events.
// NeuralModelValkeyKey is the Valkey key where trained MLP weights are stored.
const NeuralModelValkeyKey = "neural_ts:model:v1"

type ComputePolicyUseCase struct {
	lake        TelemetryLakeReader
	store       PolicyStore
	modelStore  NeuralModelStore
	publisher   PolicyEventPublisher
	audit       PolicyAuditStore
	sampler     PolicyComputer
	constraints domain.PolicyConstraints
	clock       Clock
	logger      *slog.Logger
}

// ComputePolicyConfig holds configuration for policy computation.
type ComputePolicyConfig struct {
	Lake        TelemetryLakeReader
	Store       PolicyStore
	ModelStore  NeuralModelStore
	Publisher   PolicyEventPublisher
	Audit       PolicyAuditStore
	Sampler     PolicyComputer
	Constraints domain.PolicyConstraints
	Clock       Clock
	Logger      *slog.Logger
}

// NewComputePolicyUseCase creates a new policy computation use case.
func NewComputePolicyUseCase(cfg ComputePolicyConfig) *ComputePolicyUseCase {
	clk := cfg.Clock
	if clk == nil {
		clk = RealClock{}
	}
	sampler := cfg.Sampler
	if sampler == nil {
		sampler = domain.NewThompsonSampler()
	}
	return &ComputePolicyUseCase{
		lake:        cfg.Lake,
		store:       cfg.Store,
		modelStore:  cfg.ModelStore,
		publisher:   cfg.Publisher,
		audit:       cfg.Audit,
		sampler:     sampler,
		constraints: cfg.Constraints,
		clock:       clk,
		logger:      cfg.Logger,
	}
}

// ComputeResult holds the output metrics of a policy computation run.
type ComputeResult struct {
	RunID            string
	AggregatesRead   int
	PoliciesWritten  int
	PoliciesFiltered int
	Duration         time.Duration
}

// RunHourly computes policies from the last hour of telemetry.
func (uc *ComputePolicyUseCase) RunHourly(ctx context.Context) (ComputeResult, error) {
	now := uc.clock.Now().UTC()
	from := now.Add(-1 * time.Hour)
	return uc.run(ctx, from, now, "hourly")
}

// RunDaily computes policies from the last 24 hours of telemetry.
func (uc *ComputePolicyUseCase) RunDaily(ctx context.Context) (ComputeResult, error) {
	now := uc.clock.Now().UTC()
	from := now.Add(-24 * time.Hour)
	return uc.run(ctx, from, now, "daily")
}

// RunWindow computes policies for an arbitrary time window.
func (uc *ComputePolicyUseCase) RunWindow(ctx context.Context, from, to time.Time) (ComputeResult, error) {
	return uc.run(ctx, from, to, "custom")
}

func (uc *ComputePolicyUseCase) run(ctx context.Context, from, to time.Time, schedule string) (ComputeResult, error) {
	start := time.Now()

	// Build PolicyRun for lifecycle tracking.
	run := domain.PolicyRun{
		RunID:            uuid.NewString(),
		Schedule:         schedule,
		Status:           domain.RunStatusRunning,
		AlgorithmID:      AlgorithmIDThompson,
		AlgorithmVersion: AlgorithmVersionV1,
		Window:           schedule,
		StartedAt:        uc.clock.Now().UTC(),
	}

	uc.logger.Info("policy computation started",
		"run_id", run.RunID,
		"schedule", schedule,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
	)

	// Emit run.started.
	if uc.publisher != nil {
		if err := uc.publisher.PublishRunStarted(ctx, run); err != nil {
			uc.logger.Warn("publish run.started failed", "error", err)
		}
	}

	// Helper to emit run.failed and return error.
	failRun := func(err error, code string) (ComputeResult, error) {
		run.Status = domain.RunStatusFailed
		run.CompletedAt = uc.clock.Now().UTC()
		run.DurationMs = time.Since(start).Milliseconds()
		run.ErrorCode = code
		run.ErrorMessage = err.Error()
		if uc.publisher != nil {
			if pubErr := uc.publisher.PublishRunFailed(ctx, run); pubErr != nil {
				uc.logger.Warn("publish run.failed failed", "error", pubErr)
			}
		}
		return ComputeResult{}, err
	}

	aggregates, err := uc.lake.QueryAggregates(ctx, from, to)
	if err != nil {
		return failRun(fmt.Errorf("query aggregates: %w", err), "lake_query_error")
	}

	var policies []domain.ToolPolicy
	filtered := 0

	for _, agg := range aggregates {
		policy := uc.sampler.ComputePolicy(agg.ContextSignature, agg.ToolID, agg)
		policy.FreshnessTs = uc.clock.Now().UTC()

		if !uc.constraints.IsEligible(policy) {
			filtered++
			continue
		}
		policies = append(policies, policy)
	}

	if len(policies) > 0 {
		if err := uc.store.WritePolicies(ctx, policies); err != nil {
			return failRun(fmt.Errorf("write policies: %w", err), "store_write_error")
		}
	}

	run.AggregatesRead = len(aggregates)
	run.PoliciesWritten = len(policies)
	run.PoliciesFiltered = filtered

	// Train and publish neural model when enough aggregates are available.
	if uc.modelStore != nil && len(aggregates) >= 20 {
		samples := domain.AggregatesToSamples(aggregates)
		if modelData, trainErr := domain.TrainNeuralModel(samples, domain.DefaultNeuralTrainerConfig()); trainErr != nil {
			uc.logger.Warn("neural model training failed", "error", trainErr)
		} else if writeErr := uc.modelStore.WriteNeuralModel(ctx, NeuralModelValkeyKey, modelData); writeErr != nil {
			uc.logger.Warn("neural model write failed", "error", writeErr)
		} else {
			uc.logger.Info("neural model published", "key", NeuralModelValkeyKey, "samples", len(samples))
		}
	}

	// Emit policy.computed for the batch.
	if uc.publisher != nil && len(policies) > 0 {
		if err := uc.publisher.PublishPolicyComputed(ctx, run, policies); err != nil {
			uc.logger.Warn("publish policy.computed failed", "error", err)
		}
	}

	// Write audit snapshot.
	if uc.audit != nil && len(policies) > 0 {
		if err := uc.audit.WriteSnapshot(ctx, uc.clock.Now().UTC(), policies); err != nil {
			uc.logger.Warn("audit snapshot failed", "error", err)
		} else if uc.publisher != nil {
			// Emit snapshot.published only if audit succeeded.
			snapshotRef := fmt.Sprintf("audit/%s/%s", schedule, run.RunID)
			run.SnapshotRef = snapshotRef
			if err := uc.publisher.PublishSnapshotPublished(ctx, run, snapshotRef); err != nil {
				uc.logger.Warn("publish snapshot.published failed", "error", err)
			}
		}
	}

	// Emit policy.updated (legacy, kept for backward compat).
	if uc.publisher != nil && len(policies) > 0 {
		if err := uc.publisher.PublishPolicyUpdated(ctx, policies, filtered); err != nil {
			uc.logger.Warn("publish policy.updated failed", "error", err)
		}
	}

	// Emit run.completed.
	run.Status = domain.RunStatusCompleted
	run.CompletedAt = uc.clock.Now().UTC()
	run.DurationMs = time.Since(start).Milliseconds()

	if uc.publisher != nil {
		if err := uc.publisher.PublishRunCompleted(ctx, run); err != nil {
			uc.logger.Warn("publish run.completed failed", "error", err)
		}
	}

	result := ComputeResult{
		AggregatesRead:   len(aggregates),
		PoliciesWritten:  len(policies),
		PoliciesFiltered: filtered,
		Duration:         time.Since(start),
		RunID:            run.RunID,
	}

	uc.logger.Info("policy computation completed",
		"run_id", run.RunID,
		"schedule", schedule,
		"aggregates_read", result.AggregatesRead,
		"policies_written", result.PoliciesWritten,
		"policies_filtered", result.PoliciesFiltered,
		"duration_ms", result.Duration.Milliseconds(),
	)

	return result, nil
}
