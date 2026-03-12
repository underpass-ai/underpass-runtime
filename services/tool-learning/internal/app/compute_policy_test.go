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
	published []domain.ToolPolicy
}

func (f *fakePublisher) PublishPolicyUpdated(_ context.Context, policies []domain.ToolPolicy, _ int) error {
	f.published = append(f.published, policies...)
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
