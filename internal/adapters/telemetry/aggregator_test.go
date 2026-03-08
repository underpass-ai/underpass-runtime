package telemetry

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

func TestComputeToolStats_Empty(t *testing.T) {
	stats := computeToolStats(nil)
	if stats.InvocationN != 0 {
		t.Fatalf("expected 0 invocations, got %d", stats.InvocationN)
	}
}

func TestComputeToolStats_SingleSuccess(t *testing.T) {
	records := []app.TelemetryRecord{
		{ToolName: "fs.read", Status: "succeeded", DurationMs: 100, OutputBytes: 2048},
	}
	stats := computeToolStats(records)
	if stats.SuccessRate != 1.0 {
		t.Fatalf("expected 100%% success rate, got %f", stats.SuccessRate)
	}
	if stats.P50Duration != 100 {
		t.Fatalf("expected p50=100, got %d", stats.P50Duration)
	}
	if stats.InvocationN != 1 {
		t.Fatalf("expected 1 invocation, got %d", stats.InvocationN)
	}
	if stats.DenyRate != 0 {
		t.Fatalf("expected 0 deny rate, got %f", stats.DenyRate)
	}
}

func TestComputeToolStats_MixedStatus(t *testing.T) {
	records := []app.TelemetryRecord{
		{ToolName: "fs.read", Status: "succeeded", DurationMs: 50, OutputBytes: 1024},
		{ToolName: "fs.read", Status: "succeeded", DurationMs: 100, OutputBytes: 2048},
		{ToolName: "fs.read", Status: "failed", DurationMs: 200, OutputBytes: 0},
		{ToolName: "fs.read", Status: "denied", DurationMs: 5, OutputBytes: 0},
	}
	stats := computeToolStats(records)

	// 2/4 succeeded
	if stats.SuccessRate != 0.5 {
		t.Fatalf("expected 0.5 success rate, got %f", stats.SuccessRate)
	}
	// 1/4 denied
	if stats.DenyRate != 0.25 {
		t.Fatalf("expected 0.25 deny rate, got %f", stats.DenyRate)
	}
	if stats.InvocationN != 4 {
		t.Fatalf("expected 4 invocations, got %d", stats.InvocationN)
	}
	// sorted durations: [5, 50, 100, 200]
	// p50 = index 1 = 50
	if stats.P50Duration != 50 {
		t.Fatalf("expected p50=50, got %d", stats.P50Duration)
	}
	// p95 = index 2 (int(0.95*3) = 2) = 100
	if stats.P95Duration != 100 {
		t.Fatalf("expected p95=100, got %d", stats.P95Duration)
	}
}

func TestComputeToolStats_AvgOutputKB(t *testing.T) {
	records := []app.TelemetryRecord{
		{Status: "succeeded", OutputBytes: 1024},
		{Status: "succeeded", OutputBytes: 3072},
	}
	stats := computeToolStats(records)
	// avg = (1024 + 3072) / 2 / 1024 = 2.0 KB
	if stats.AvgOutputKB != 2.0 {
		t.Fatalf("expected 2.0 KB avg output, got %f", stats.AvgOutputKB)
	}
}

func TestPercentile_Empty(t *testing.T) {
	if p := percentile(nil, 0.50); p != 0 {
		t.Fatalf("expected 0, got %d", p)
	}
}

func TestPercentile_SingleValue(t *testing.T) {
	if p := percentile([]int64{42}, 0.50); p != 42 {
		t.Fatalf("expected 42, got %d", p)
	}
	if p := percentile([]int64{42}, 0.95); p != 42 {
		t.Fatalf("expected 42, got %d", p)
	}
}

func TestPercentile_MultipleValues(t *testing.T) {
	sorted := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50 := percentile(sorted, 0.50)
	if p50 != 50 {
		t.Fatalf("expected p50=50, got %d", p50)
	}
	p95 := percentile(sorted, 0.95)
	// int(0.95 * 9) = 8, sorted[8] = 90
	if p95 != 90 {
		t.Fatalf("expected p95=90, got %d", p95)
	}
}

func TestRoundTo(t *testing.T) {
	tests := []struct {
		val      float64
		decimals int
		expected float64
	}{
		{0.12345, 2, 0.12},
		{0.12345, 4, 0.1235},
		{1.0, 2, 1.0},
		{0.999, 2, 1.0},
	}
	for _, tt := range tests {
		got := roundTo(tt.val, tt.decimals)
		if got != tt.expected {
			t.Errorf("roundTo(%f, %d) = %f, want %f", tt.val, tt.decimals, got, tt.expected)
		}
	}
}

func TestInMemoryAggregator_RecordAndQuery(t *testing.T) {
	agg := NewInMemoryAggregator()
	ctx := context.Background()

	if err := agg.Record(ctx, app.TelemetryRecord{
		ToolName:    "fs.read",
		Status:      "succeeded",
		DurationMs:  100,
		OutputBytes: 2048,
	}); err != nil {
		t.Fatalf("record error: %v", err)
	}

	stats, ok, err := agg.ToolStats(ctx, "fs.read")
	if err != nil {
		t.Fatalf("stats error: %v", err)
	}
	if !ok {
		t.Fatal("expected stats to exist")
	}
	if stats.SuccessRate != 1.0 {
		t.Fatalf("expected 100%% success, got %f", stats.SuccessRate)
	}
	if stats.InvocationN != 1 {
		t.Fatalf("expected 1 invocation, got %d", stats.InvocationN)
	}
}

func TestInMemoryAggregator_Missing(t *testing.T) {
	agg := NewInMemoryAggregator()
	_, ok, err := agg.ToolStats(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestInMemoryAggregator_AllToolStats(t *testing.T) {
	agg := NewInMemoryAggregator()
	ctx := context.Background()

	_ = agg.Record(ctx, app.TelemetryRecord{ToolName: "fs.read", Status: "succeeded", DurationMs: 10})
	_ = agg.Record(ctx, app.TelemetryRecord{ToolName: "fs.write", Status: "failed", DurationMs: 20})

	allStats, err := agg.AllToolStats(ctx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(allStats) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(allStats))
	}
	if _, ok := allStats["fs.read"]; !ok {
		t.Fatal("missing fs.read stats")
	}
	if _, ok := allStats["fs.write"]; !ok {
		t.Fatal("missing fs.write stats")
	}
}

func TestAggregator_StartStop(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agg := NewAggregator(rec, logger, WithAggregationInterval(50*time.Millisecond))

	// Register a tool and add data
	if err := rec.Record(context.Background(), sampleRecord("fs.read", "succeeded")); err != nil {
		t.Fatalf("record error: %v", err)
	}
	agg.RegisterTool("fs.read")

	agg.Start()
	// Start again should be no-op
	agg.Start()

	// Wait for at least one aggregation cycle
	time.Sleep(150 * time.Millisecond)

	stats, ok, err := agg.ToolStats(context.Background(), "fs.read")
	if err != nil {
		t.Fatalf("stats error: %v", err)
	}
	if !ok {
		t.Fatal("expected stats for fs.read")
	}
	if stats.InvocationN != 1 {
		t.Fatalf("expected 1 invocation, got %d", stats.InvocationN)
	}

	agg.Stop()
	// Stop again should be no-op
	agg.Stop()
}

func TestAggregator_StopWithoutStart(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agg := NewAggregator(rec, logger)
	// Should not panic
	agg.Stop()
}

func TestAggregator_RegisterTool(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agg := NewAggregator(rec, logger)

	agg.RegisterTool("fs.read")
	agg.RegisterTool("fs.read") // duplicate should be no-op

	allStats, err := agg.AllToolStats(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if _, ok := allStats["fs.read"]; !ok {
		t.Fatal("expected fs.read to be registered")
	}
}

func TestAggregator_AllToolStats_Empty(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agg := NewAggregator(rec, logger)

	allStats, err := agg.AllToolStats(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(allStats) != 0 {
		t.Fatalf("expected empty, got %d", len(allStats))
	}
}

func TestAggregator_WithAggregationInterval(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Invalid (zero) interval should keep default
	agg := NewAggregator(rec, logger, WithAggregationInterval(0))
	if agg.interval != 5*time.Minute {
		t.Fatalf("expected default 5m interval, got %v", agg.interval)
	}

	// Valid interval
	agg = NewAggregator(rec, logger, WithAggregationInterval(30*time.Second))
	if agg.interval != 30*time.Second {
		t.Fatalf("expected 30s interval, got %v", agg.interval)
	}
}

func TestAggregator_AggregateReadError(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agg := NewAggregator(rec, logger, WithAggregationInterval(50*time.Millisecond))

	agg.RegisterTool("fs.read")
	client.rangeErr = errTimeout

	agg.Start()
	time.Sleep(100 * time.Millisecond)
	agg.Stop()

	// Should not panic; stats should be empty due to read error
	allStats, _ := agg.AllToolStats(context.Background())
	if len(allStats) != 0 {
		t.Fatalf("expected no stats on read error, got %d", len(allStats))
	}
}

var errTimeout = context.DeadlineExceeded
