package telemetry

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// Aggregator computes per-tool stats from telemetry records. It periodically
// reads raw records from the recorder and updates in-memory stats that can
// be queried by the discovery endpoint and recommendation engine.
type Aggregator struct {
	recorder *ValkeyRecorder
	logger   *slog.Logger
	interval time.Duration

	mu    sync.RWMutex
	stats map[string]app.ToolStats

	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// AggregatorOption configures the aggregator.
type AggregatorOption func(*Aggregator)

// WithAggregationInterval sets how often the aggregator recomputes stats.
func WithAggregationInterval(d time.Duration) AggregatorOption {
	return func(a *Aggregator) {
		if d > 0 {
			a.interval = d
		}
	}
}

// NewAggregator creates an Aggregator that reads from the given recorder.
func NewAggregator(recorder *ValkeyRecorder, logger *slog.Logger, opts ...AggregatorOption) *Aggregator {
	a := &Aggregator{
		recorder: recorder,
		logger:   logger,
		interval: 5 * time.Minute,
		stats:    map[string]app.ToolStats{},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Start begins the background aggregation loop. Safe to call multiple times;
// subsequent calls are no-ops.
func (a *Aggregator) Start() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return
	}
	a.running = true
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.done = make(chan struct{})
	go a.loop(ctx)
}

// Stop halts the aggregation loop and waits for it to finish.
func (a *Aggregator) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.cancel()
	a.running = false
	done := a.done
	a.mu.Unlock()
	<-done
}

// ToolStats returns aggregated stats for a single tool.
func (a *Aggregator) ToolStats(_ context.Context, toolName string) (app.ToolStats, bool, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.stats[toolName]
	return s, ok, nil
}

// AllToolStats returns a snapshot of all per-tool stats.
func (a *Aggregator) AllToolStats(_ context.Context) (map[string]app.ToolStats, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]app.ToolStats, len(a.stats))
	for k, v := range a.stats {
		out[k] = v
	}
	return out, nil
}

func (a *Aggregator) loop(ctx context.Context) {
	defer close(a.done)
	// Run immediately on start.
	a.aggregate(ctx)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.aggregate(ctx)
		}
	}
}

// aggregate reads all known tool keys and recomputes stats.
func (a *Aggregator) aggregate(ctx context.Context) {
	a.mu.RLock()
	tools := make([]string, 0, len(a.stats))
	for t := range a.stats {
		tools = append(tools, t)
	}
	a.mu.RUnlock()

	// Also discover tools from the recorder's Valkey lists. Since we don't
	// have a KEYS/SCAN on the minimal interface, rely on the stats map
	// which is seeded by incoming Record() calls.

	newStats := make(map[string]app.ToolStats, len(tools))
	for _, toolName := range tools {
		records, err := a.recorder.ReadTool(ctx, toolName)
		if err != nil {
			a.logger.Warn("telemetry aggregator read failed", "tool", toolName, "error", err)
			continue
		}
		if len(records) == 0 {
			continue
		}
		newStats[toolName] = computeToolStats(records)
	}

	a.mu.Lock()
	a.stats = newStats
	a.mu.Unlock()
}

// RegisterTool ensures the tool is tracked by the aggregator. Called when
// a new telemetry record is written so the aggregator knows about new tools.
func (a *Aggregator) RegisterTool(toolName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.stats[toolName]; !ok {
		a.stats[toolName] = app.ToolStats{}
	}
}

// computeToolStats derives ToolStats from a slice of telemetry records.
func computeToolStats(records []app.TelemetryRecord) app.ToolStats {
	if len(records) == 0 {
		return app.ToolStats{}
	}

	total := len(records)
	succeeded := 0
	denied := 0
	var durations []int64
	var totalOutputBytes int64

	for i := range records {
		if records[i].Status == "succeeded" {
			succeeded++
		}
		if records[i].Status == "denied" {
			denied++
		}
		durations = append(durations, records[i].DurationMs)
		totalOutputBytes += records[i].OutputBytes
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	successRate := float64(succeeded) / float64(total)
	denyRate := float64(denied) / float64(total)
	avgOutputKB := float64(totalOutputBytes) / float64(total) / 1024.0

	return app.ToolStats{
		SuccessRate: roundTo(successRate, 4),
		P50Duration: percentile(durations, 0.50),
		P95Duration: percentile(durations, 0.95),
		AvgOutputKB: roundTo(avgOutputKB, 2),
		DenyRate:    roundTo(denyRate, 4),
		InvocationN: total,
	}
}

// percentile returns the value at the given percentile (0-1) from a sorted slice.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// roundTo rounds a float to the given number of decimal places.
func roundTo(val float64, decimals int) float64 {
	shift := 1.0
	for range decimals {
		shift *= 10
	}
	return float64(int64(val*shift+0.5)) / shift
}

// InMemoryAggregator is an in-memory implementation of TelemetryQuerier for
// standalone mode. Stats are updated directly without a background loop.
type InMemoryAggregator struct {
	mu      sync.RWMutex
	records map[string][]app.TelemetryRecord
}

// NewInMemoryAggregator creates a standalone in-memory aggregator.
func NewInMemoryAggregator() *InMemoryAggregator {
	return &InMemoryAggregator{records: map[string][]app.TelemetryRecord{}}
}

// Record adds a telemetry record and recomputes stats lazily.
func (m *InMemoryAggregator) Record(_ context.Context, record app.TelemetryRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	toolName := strings.TrimSpace(record.ToolName)
	m.records[toolName] = append(m.records[toolName], record)
	return nil
}

// ToolStats returns computed stats for a single tool.
func (m *InMemoryAggregator) ToolStats(_ context.Context, toolName string) (app.ToolStats, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	recs, ok := m.records[toolName]
	if !ok || len(recs) == 0 {
		return app.ToolStats{}, false, nil
	}
	return computeToolStats(recs), true, nil
}

// AllToolStats returns a snapshot of all per-tool stats.
func (m *InMemoryAggregator) AllToolStats(_ context.Context) (map[string]app.ToolStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]app.ToolStats, len(m.records))
	for toolName, recs := range m.records {
		if len(recs) > 0 {
			out[toolName] = computeToolStats(recs)
		}
	}
	return out, nil
}
