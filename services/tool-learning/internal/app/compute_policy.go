package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// ComputePolicyUseCase reads telemetry from the lake, computes Thompson Sampling
// policies, persists them, and publishes update events.
type ComputePolicyUseCase struct {
	lake        TelemetryLakeReader
	store       PolicyStore
	publisher   PolicyEventPublisher
	audit       PolicyAuditStore
	sampler     *domain.ThompsonSampler
	constraints domain.PolicyConstraints
	clock       Clock
	logger      *slog.Logger
}

// ComputePolicyConfig holds configuration for policy computation.
type ComputePolicyConfig struct {
	Lake        TelemetryLakeReader
	Store       PolicyStore
	Publisher   PolicyEventPublisher
	Audit       PolicyAuditStore
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
	return &ComputePolicyUseCase{
		lake:        cfg.Lake,
		store:       cfg.Store,
		publisher:   cfg.Publisher,
		audit:       cfg.Audit,
		sampler:     domain.NewThompsonSampler(),
		constraints: cfg.Constraints,
		clock:       clk,
		logger:      cfg.Logger,
	}
}

// ComputeResult holds the output metrics of a policy computation run.
type ComputeResult struct {
	AggregatesRead int
	PoliciesWritten int
	PoliciesFiltered int
	Duration        time.Duration
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
	uc.logger.Info("policy computation started",
		"schedule", schedule,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
	)

	aggregates, err := uc.lake.QueryAggregates(ctx, from, to)
	if err != nil {
		return ComputeResult{}, fmt.Errorf("query aggregates: %w", err)
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
			return ComputeResult{}, fmt.Errorf("write policies: %w", err)
		}
	}

	if uc.audit != nil && len(policies) > 0 {
		if err := uc.audit.WriteSnapshot(ctx, uc.clock.Now().UTC(), policies); err != nil {
			uc.logger.Warn("audit snapshot failed", "error", err)
		}
	}

	if uc.publisher != nil && len(policies) > 0 {
		if err := uc.publisher.PublishPolicyUpdated(ctx, policies); err != nil {
			uc.logger.Warn("publish policy update failed", "error", err)
		}
	}

	result := ComputeResult{
		AggregatesRead:   len(aggregates),
		PoliciesWritten:  len(policies),
		PoliciesFiltered: filtered,
		Duration:         time.Since(start),
	}

	uc.logger.Info("policy computation completed",
		"schedule", schedule,
		"aggregates_read", result.AggregatesRead,
		"policies_written", result.PoliciesWritten,
		"policies_filtered", result.PoliciesFiltered,
		"duration_ms", result.Duration.Milliseconds(),
	)

	return result, nil
}
