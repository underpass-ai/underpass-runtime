package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"log/slog"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// --- Fakes ---

type fakeClock struct{ now time.Time }

func (f fakeClock) Now() time.Time { return f.now }

type fakeLakeReader struct {
	aggregates []domain.AggregateStats
	err        error
}

func (f *fakeLakeReader) QueryAggregates(_ context.Context, _, _ time.Time) ([]domain.AggregateStats, error) {
	return f.aggregates, f.err
}

type fakePolicyStore struct {
	written []domain.ToolPolicy
}

func (f *fakePolicyStore) WritePolicy(_ context.Context, p domain.ToolPolicy) error {
	f.written = append(f.written, p)
	return nil
}

func (f *fakePolicyStore) WritePolicies(_ context.Context, policies []domain.ToolPolicy) error {
	f.written = append(f.written, policies...)
	return nil
}

func (f *fakePolicyStore) ReadPolicy(_ context.Context, contextSig, toolID string) (domain.ToolPolicy, bool, error) {
	for _, p := range f.written {
		if p.ContextSignature == contextSig && p.ToolID == toolID {
			return p, true, nil
		}
	}
	return domain.ToolPolicy{}, false, nil
}

type fakePublisher struct {
	published     []domain.ToolPolicy
	runsStarted   []domain.PolicyRun
	runsCompleted []domain.PolicyRun
	runsFailed    []domain.PolicyRun
	computed      []domain.ToolPolicy
	snapshots     []string
}

func (f *fakePublisher) PublishPolicyUpdated(_ context.Context, policies []domain.ToolPolicy, _ int) error {
	f.published = append(f.published, policies...)
	return nil
}
func (f *fakePublisher) PublishRunStarted(_ context.Context, run domain.PolicyRun) error {
	f.runsStarted = append(f.runsStarted, run)
	return nil
}
func (f *fakePublisher) PublishRunCompleted(_ context.Context, run domain.PolicyRun) error {
	f.runsCompleted = append(f.runsCompleted, run)
	return nil
}
func (f *fakePublisher) PublishRunFailed(_ context.Context, run domain.PolicyRun) error {
	f.runsFailed = append(f.runsFailed, run)
	return nil
}
func (f *fakePublisher) PublishPolicyComputed(_ context.Context, _ domain.PolicyRun, policies []domain.ToolPolicy) error {
	f.computed = append(f.computed, policies...)
	return nil
}
func (f *fakePublisher) PublishSnapshotPublished(_ context.Context, _ domain.PolicyRun, ref string) error {
	f.snapshots = append(f.snapshots, ref)
	return nil
}

type fakeAudit struct {
	snapshots int
}

func (f *fakeAudit) WriteSnapshot(_ context.Context, _ time.Time, _ []domain.ToolPolicy) error {
	f.snapshots++
	return nil
}

type failingPolicyStore struct{}

func (f *failingPolicyStore) WritePolicy(_ context.Context, _ domain.ToolPolicy) error {
	return fmt.Errorf("store unavailable")
}
func (f *failingPolicyStore) WritePolicies(_ context.Context, _ []domain.ToolPolicy) error {
	return fmt.Errorf("store unavailable")
}
func (f *failingPolicyStore) ReadPolicy(_ context.Context, _, _ string) (domain.ToolPolicy, bool, error) {
	return domain.ToolPolicy{}, false, fmt.Errorf("store unavailable")
}

type failingAudit struct{}

func (f *failingAudit) WriteSnapshot(_ context.Context, _ time.Time, _ []domain.ToolPolicy) error {
	return fmt.Errorf("audit unavailable")
}

type failingPublisher struct{}

func (f *failingPublisher) PublishPolicyUpdated(_ context.Context, _ []domain.ToolPolicy, _ int) error {
	return fmt.Errorf("nats unavailable")
}
func (f *failingPublisher) PublishRunStarted(_ context.Context, _ domain.PolicyRun) error {
	return fmt.Errorf("nats unavailable")
}
func (f *failingPublisher) PublishRunCompleted(_ context.Context, _ domain.PolicyRun) error {
	return fmt.Errorf("nats unavailable")
}
func (f *failingPublisher) PublishRunFailed(_ context.Context, _ domain.PolicyRun) error {
	return fmt.Errorf("nats unavailable")
}
func (f *failingPublisher) PublishPolicyComputed(_ context.Context, _ domain.PolicyRun, _ []domain.ToolPolicy) error {
	return fmt.Errorf("nats unavailable")
}
func (f *failingPublisher) PublishSnapshotPublished(_ context.Context, _ domain.PolicyRun, _ string) error {
	return fmt.Errorf("nats unavailable")
}

type fakePolicyComputer struct {
	calls int
}

func (f *fakePolicyComputer) ComputePolicy(contextSig, toolID string, stats domain.AggregateStats) domain.ToolPolicy {
	f.calls++
	return domain.ToolPolicy{
		ContextSignature: contextSig,
		ToolID:           toolID,
		Alpha:            999,
		Beta:             1,
		Confidence:       0.999,
		P95LatencyMs:     stats.P95LatencyMs,
		P95Cost:          stats.P95Cost,
		ErrorRate:        stats.ErrorRate,
		NSamples:         stats.Total,
	}
}

// --- Tests ---

func TestComputePolicyRunHourly(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
			{ContextSignature: "gen:go:std", ToolID: "fs.read", Total: 100, Successes: 99, Failures: 1, P95LatencyMs: 50, P95Cost: 0.01, ErrorRate: 0.01},
		},
	}
	store := &fakePolicyStore{}
	pub := &fakePublisher{}
	audit := &fakeAudit{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if result.AggregatesRead != 2 {
		t.Errorf("AggregatesRead = %d, want 2", result.AggregatesRead)
	}
	if result.PoliciesWritten != 2 {
		t.Errorf("PoliciesWritten = %d, want 2", result.PoliciesWritten)
	}
	if len(store.written) != 2 {
		t.Errorf("store has %d policies, want 2", len(store.written))
	}
	if len(pub.published) != 2 {
		t.Errorf("published %d policies, want 2", len(pub.published))
	}
	if audit.snapshots != 1 {
		t.Errorf("audit snapshots = %d, want 1", audit.snapshots)
	}
}

func TestComputePolicyFiltersExceedingSLO(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fast-tool", Total: 100, Successes: 95, Failures: 5, P95LatencyMs: 100, P95Cost: 0.1, ErrorRate: 0.05},
			{ContextSignature: "gen:go:std", ToolID: "slow-tool", Total: 100, Successes: 95, Failures: 5, P95LatencyMs: 10000, P95Cost: 0.1, ErrorRate: 0.05},
		},
	}
	store := &fakePolicyStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:  lake,
		Store: store,
		Constraints: domain.PolicyConstraints{
			MaxP95LatencyMs: 5000,
		},
		Clock:  fakeClock{now: now},
		Logger: slog.Default(),
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if result.PoliciesWritten != 1 {
		t.Errorf("PoliciesWritten = %d, want 1 (slow-tool filtered)", result.PoliciesWritten)
	}
	if result.PoliciesFiltered != 1 {
		t.Errorf("PoliciesFiltered = %d, want 1", result.PoliciesFiltered)
	}
}

func TestComputePolicyEmptyLake(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{aggregates: nil}
	store := &fakePolicyStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:   lake,
		Store:  store,
		Clock:  fakeClock{now: now},
		Logger: slog.Default(),
	})

	result, err := uc.RunDaily(context.Background())
	if err != nil {
		t.Fatalf("RunDaily() error: %v", err)
	}
	if result.PoliciesWritten != 0 {
		t.Errorf("PoliciesWritten = %d, want 0", result.PoliciesWritten)
	}
}

func TestRealClock(t *testing.T) {
	c := RealClock{}
	now := c.Now()
	if time.Since(now) > time.Second {
		t.Errorf("RealClock.Now() returned stale time: %v", now)
	}
}

func TestNewComputePolicyUseCaseDefaultClock(t *testing.T) {
	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:   &fakeLakeReader{},
		Store:  &fakePolicyStore{},
		Logger: slog.Default(),
	})
	// clock should default to RealClock when not provided
	now := uc.clock.Now()
	if time.Since(now) > time.Second {
		t.Error("default clock should use RealClock")
	}
}

func TestComputePolicyLakeError(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{err: fmt.Errorf("connection refused")}
	store := &fakePolicyStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:   lake,
		Store:  store,
		Clock:  fakeClock{now: now},
		Logger: slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err == nil {
		t.Fatal("expected error from lake failure")
	}
}

func TestComputePolicyStoreError(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &failingPolicyStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:   lake,
		Store:  store,
		Clock:  fakeClock{now: now},
		Logger: slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err == nil {
		t.Fatal("expected error from store failure")
	}
}

func TestComputePolicyAuditAndPublishErrors(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &fakePolicyStore{}
	audit := &failingAudit{}
	pub := &failingPublisher{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Audit:     audit,
		Publisher: pub,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	// Should succeed — audit/publish errors are logged but don't fail the run.
	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}
	if result.PoliciesWritten != 1 {
		t.Errorf("PoliciesWritten = %d, want 1", result.PoliciesWritten)
	}
}

func TestComputePolicyWithCustomSampler(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &fakePolicyStore{}
	sampler := &fakePolicyComputer{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:    lake,
		Store:   store,
		Sampler: sampler,
		Clock:   fakeClock{now: now},
		Logger:  slog.Default(),
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}
	if result.PoliciesWritten != 1 {
		t.Errorf("PoliciesWritten = %d, want 1", result.PoliciesWritten)
	}
	if sampler.calls != 1 {
		t.Errorf("sampler.calls = %d, want 1", sampler.calls)
	}
	if store.written[0].Alpha != 999 {
		t.Errorf("Alpha = %f, want 999 (from custom sampler)", store.written[0].Alpha)
	}
}

// --- P1 lifecycle event tests ---

func TestComputePolicyEmitsRunLifecycle(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &fakePolicyStore{}
	pub := &fakePublisher{}
	audit := &fakeAudit{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if result.RunID == "" {
		t.Fatal("expected non-empty RunID")
	}
	if len(pub.runsStarted) != 1 {
		t.Fatalf("expected 1 run.started event, got %d", len(pub.runsStarted))
	}
	if pub.runsStarted[0].RunID != result.RunID {
		t.Fatalf("run.started RunID mismatch")
	}
	if pub.runsStarted[0].Status != domain.RunStatusRunning {
		t.Fatalf("run.started status = %s, want running", pub.runsStarted[0].Status)
	}
	if len(pub.runsCompleted) != 1 {
		t.Fatalf("expected 1 run.completed event, got %d", len(pub.runsCompleted))
	}
	if pub.runsCompleted[0].PoliciesWritten != 1 {
		t.Fatalf("run.completed policies_written = %d, want 1", pub.runsCompleted[0].PoliciesWritten)
	}
	if len(pub.runsFailed) != 0 {
		t.Fatalf("expected 0 run.failed events, got %d", len(pub.runsFailed))
	}
}

func TestComputePolicyEmitsPolicyComputed(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
			{ContextSignature: "gen:go:std", ToolID: "fs.read", Total: 100, Successes: 99, Failures: 1, P95LatencyMs: 50, P95Cost: 0.01, ErrorRate: 0.01},
		},
	}
	store := &fakePolicyStore{}
	pub := &fakePublisher{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if len(pub.computed) != 2 {
		t.Fatalf("expected 2 policy.computed events, got %d", len(pub.computed))
	}
}

func TestComputePolicyEmitsSnapshotPublished(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &fakePolicyStore{}
	pub := &fakePublisher{}
	audit := &fakeAudit{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if len(pub.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot.published event, got %d", len(pub.snapshots))
	}
	if pub.snapshots[0] == "" {
		t.Fatal("snapshot ref should not be empty")
	}
}

func TestComputePolicyEmitsRunFailedOnLakeError(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{err: fmt.Errorf("connection refused")}
	store := &fakePolicyStore{}
	pub := &fakePublisher{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	if len(pub.runsStarted) != 1 {
		t.Fatalf("expected 1 run.started, got %d", len(pub.runsStarted))
	}
	if len(pub.runsFailed) != 1 {
		t.Fatalf("expected 1 run.failed, got %d", len(pub.runsFailed))
	}
	if pub.runsFailed[0].ErrorCode != "lake_query_error" {
		t.Fatalf("expected error_code=lake_query_error, got %s", pub.runsFailed[0].ErrorCode)
	}
	if len(pub.runsCompleted) != 0 {
		t.Fatalf("expected 0 run.completed, got %d", len(pub.runsCompleted))
	}
}

func TestComputePolicyEmitsRunFailedOnStoreError(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Total: 50, Successes: 45, Failures: 5, P95LatencyMs: 200, P95Cost: 0.1, ErrorRate: 0.1},
		},
	}
	store := &failingPolicyStore{}
	pub := &fakePublisher{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Clock:     fakeClock{now: now},
		Logger:    slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	if len(pub.runsFailed) != 1 {
		t.Fatalf("expected 1 run.failed, got %d", len(pub.runsFailed))
	}
	if pub.runsFailed[0].ErrorCode != "store_write_error" {
		t.Fatalf("expected error_code=store_write_error, got %s", pub.runsFailed[0].ErrorCode)
	}
}

func TestComputePolicyRunWindow(t *testing.T) {
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	lake := &fakeLakeReader{
		aggregates: []domain.AggregateStats{
			{ContextSignature: "gen:py:std", ToolID: "git.commit", Total: 200, Successes: 190, Failures: 10, P95LatencyMs: 500, P95Cost: 0.2, ErrorRate: 0.05},
		},
	}
	store := &fakePolicyStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:   lake,
		Store:  store,
		Clock:  fakeClock{now: to},
		Logger: slog.Default(),
	})

	result, err := uc.RunWindow(context.Background(), from, to)
	if err != nil {
		t.Fatalf("RunWindow() error: %v", err)
	}
	if result.PoliciesWritten != 1 {
		t.Errorf("PoliciesWritten = %d, want 1", result.PoliciesWritten)
	}
	if store.written[0].Alpha != 191.0 {
		t.Errorf("Alpha = %f, want 191.0", store.written[0].Alpha)
	}
}

// --- Neural model training ---

type fakeNeuralModelStore struct {
	key      string
	data     []byte
	writeErr error
}

func (f *fakeNeuralModelStore) WriteNeuralModel(_ context.Context, key string, data []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.key = key
	f.data = data
	return nil
}

func TestComputePolicyTrainsAndPublishesNeuralModel(t *testing.T) {
	// 20+ aggregates needed to trigger training
	aggregates := make([]domain.AggregateStats, 25)
	for i := range aggregates {
		aggregates[i] = domain.AggregateStats{
			ContextSignature: "io:go:standard",
			ToolID:           fmt.Sprintf("tool.%d", i),
			Total:            100,
			Successes:        90,
			Failures:         10,
			P95LatencyMs:     50,
			ErrorRate:        0.1,
		}
	}

	lake := &fakeLakeReader{aggregates: aggregates}
	store := &fakePolicyStore{}
	modelStore := &fakeNeuralModelStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:       lake,
		Store:      store,
		ModelStore: modelStore,
		Clock:      fakeClock{now: time.Now().UTC()},
		Logger:     slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if modelStore.key != NeuralModelValkeyKey {
		t.Fatalf("expected model key %s, got %s", NeuralModelValkeyKey, modelStore.key)
	}
	if len(modelStore.data) == 0 {
		t.Fatal("expected non-empty model data")
	}
}

func TestComputePolicySkipsNeuralModelWhenTooFewAggregates(t *testing.T) {
	// Only 5 aggregates — below the 20 threshold
	aggregates := make([]domain.AggregateStats, 5)
	for i := range aggregates {
		aggregates[i] = domain.AggregateStats{
			ContextSignature: "io:go:standard",
			ToolID:           fmt.Sprintf("tool.%d", i),
			Total:            100,
			Successes:        90,
		}
	}

	lake := &fakeLakeReader{aggregates: aggregates}
	store := &fakePolicyStore{}
	modelStore := &fakeNeuralModelStore{}

	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:       lake,
		Store:      store,
		ModelStore: modelStore,
		Clock:      fakeClock{now: time.Now().UTC()},
		Logger:     slog.Default(),
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error: %v", err)
	}

	if modelStore.key != "" {
		t.Fatal("expected no model written when < 20 aggregates")
	}
}

func TestComputePolicyNeuralModelWriteError(t *testing.T) {
	aggregates := make([]domain.AggregateStats, 25)
	for i := range aggregates {
		aggregates[i] = domain.AggregateStats{
			ContextSignature: "io:go:standard",
			ToolID:           fmt.Sprintf("tool.%d", i),
			Total:            100,
			Successes:        90,
		}
	}

	modelStore := &fakeNeuralModelStore{writeErr: fmt.Errorf("valkey down")}
	uc := NewComputePolicyUseCase(ComputePolicyConfig{
		Lake:       &fakeLakeReader{aggregates: aggregates},
		Store:      &fakePolicyStore{},
		ModelStore: modelStore,
		Clock:      fakeClock{now: time.Now().UTC()},
		Logger:     slog.Default(),
	})

	// Should succeed despite model write failure (non-fatal)
	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("expected no error despite model write failure: %v", err)
	}
}
