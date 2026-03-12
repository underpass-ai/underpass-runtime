//go:build integration

// Package integration runs end-to-end pipeline tests using embedded
// services (miniredis, embedded NATS, in-memory DuckDB) with real
// adapter wiring. No external containers required.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/marcboeker/go-duckdb"
	natsserver "github.com/nats-io/nats-server/v2/server"
	gonats "github.com/nats-io/nats.go"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/duckdb"
	natspub "github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/nats"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/valkey"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/app"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// fixedClock returns a deterministic time for reproducible tests.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// captureAudit records WriteSnapshot calls in-memory for verification.
type captureAudit struct {
	mu        sync.Mutex
	snapshots []auditCapture
}

type auditCapture struct {
	Ts       time.Time
	Policies []domain.ToolPolicy
}

func (c *captureAudit) WriteSnapshot(_ context.Context, ts time.Time, policies []domain.ToolPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]domain.ToolPolicy, len(policies))
	copy(cp, policies)
	c.snapshots = append(c.snapshots, auditCapture{Ts: ts, Policies: cp})
	return nil
}

func startMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	return miniredis.RunT(t)
}

func startNATS(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv
}

func seedInvocations(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE invocations (
		invocation_id VARCHAR, ts TIMESTAMP, tool_id VARCHAR,
		context_signature VARCHAR, outcome VARCHAR,
		latency_ms BIGINT, cost_units DOUBLE
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// 2 contexts × 2-3 tools = 3 combos, 30 invocations total
	// fs.write: 8 success + 2 failure = 80% success
	// fs.read:  10 success + 0 failure = 100% success
	// git.commit: 5 success + 5 failure = 50% success
	rows := []struct {
		id, tool, ctx, outcome string
		latency                int
		cost                   float64
	}{
		{"i01", "fs.write", "gen:go:std", "success", 100, 0.10},
		{"i02", "fs.write", "gen:go:std", "success", 120, 0.12},
		{"i03", "fs.write", "gen:go:std", "success", 110, 0.11},
		{"i04", "fs.write", "gen:go:std", "success", 130, 0.13},
		{"i05", "fs.write", "gen:go:std", "success", 90, 0.09},
		{"i06", "fs.write", "gen:go:std", "success", 140, 0.14},
		{"i07", "fs.write", "gen:go:std", "success", 105, 0.10},
		{"i08", "fs.write", "gen:go:std", "success", 115, 0.11},
		{"i09", "fs.write", "gen:go:std", "failure", 500, 0.50},
		{"i10", "fs.write", "gen:go:std", "failure", 600, 0.60},
		{"i11", "fs.read", "gen:go:std", "success", 30, 0.03},
		{"i12", "fs.read", "gen:go:std", "success", 40, 0.04},
		{"i13", "fs.read", "gen:go:std", "success", 35, 0.03},
		{"i14", "fs.read", "gen:go:std", "success", 50, 0.05},
		{"i15", "fs.read", "gen:go:std", "success", 25, 0.02},
		{"i16", "fs.read", "gen:go:std", "success", 45, 0.04},
		{"i17", "fs.read", "gen:go:std", "success", 32, 0.03},
		{"i18", "fs.read", "gen:go:std", "success", 38, 0.03},
		{"i19", "fs.read", "gen:go:std", "success", 42, 0.04},
		{"i20", "fs.read", "gen:go:std", "success", 28, 0.02},
		{"i21", "git.commit", "review:go:strict", "success", 200, 0.20},
		{"i22", "git.commit", "review:go:strict", "success", 210, 0.21},
		{"i23", "git.commit", "review:go:strict", "success", 190, 0.19},
		{"i24", "git.commit", "review:go:strict", "success", 220, 0.22},
		{"i25", "git.commit", "review:go:strict", "success", 180, 0.18},
		{"i26", "git.commit", "review:go:strict", "failure", 800, 0.80},
		{"i27", "git.commit", "review:go:strict", "failure", 900, 0.90},
		{"i28", "git.commit", "review:go:strict", "failure", 750, 0.75},
		{"i29", "git.commit", "review:go:strict", "failure", 850, 0.85},
		{"i30", "git.commit", "review:go:strict", "failure", 950, 0.95},
	}

	ts := now.Add(-30 * time.Minute)
	for _, r := range rows {
		_, err := db.Exec(
			"INSERT INTO invocations VALUES (?, ?, ?, ?, ?, ?, ?)",
			r.id, ts.Format("2006-01-02 15:04:05"), r.tool, r.ctx, r.outcome, r.latency, r.cost,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
		ts = ts.Add(time.Minute)
	}
}

// TestFullPipelineHourly runs the complete tool-learning pipeline:
// seed DuckDB → compute policies → verify Valkey writes, audit capture, NATS event.
func TestFullPipelineHourly(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)

	// --- DuckDB lake (in-memory) ---
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb: %v", err)
	}
	defer db.Close()
	seedInvocations(t, db, now)
	lake, err := duckdb.NewLakeReader(db, "invocations")
	if err != nil {
		t.Fatalf("NewLakeReader: %v", err)
	}

	// --- Valkey (miniredis) ---
	redisSrv := startMiniredis(t)
	store, err := valkey.NewPolicyStoreFromAddress(
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute,
	)
	if err != nil {
		t.Fatalf("valkey store: %v", err)
	}

	// --- NATS (embedded) ---
	natsSrv := startNATS(t)
	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer conn.Close()
	pub := natspub.NewPublisher(conn, "hourly")

	// Subscribe to capture the event
	var eventReceived []byte
	var eventMu sync.Mutex
	eventCh := make(chan struct{}, 1)
	sub, err := conn.Subscribe("tool_learning.policy.updated", func(msg *gonats.Msg) {
		eventMu.Lock()
		eventReceived = msg.Data
		eventMu.Unlock()
		select {
		case eventCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("nats subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// --- Audit (capture) ---
	audit := &captureAudit{}

	// --- Run pipeline ---
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Clock:     fixedClock{t: now},
		Logger:    logger,
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	// --- Verify result metrics ---
	if result.AggregatesRead != 3 {
		t.Errorf("AggregatesRead = %d, want 3", result.AggregatesRead)
	}
	if result.PoliciesWritten != 3 {
		t.Errorf("PoliciesWritten = %d, want 3", result.PoliciesWritten)
	}
	if result.PoliciesFiltered != 0 {
		t.Errorf("PoliciesFiltered = %d, want 0", result.PoliciesFiltered)
	}

	// --- Verify Valkey: policies are persisted ---
	ctx := context.Background()

	// fs.write in gen:go:std — Alpha = 8+1=9, Beta = 2+1=3
	p1, found, err := store.ReadPolicy(ctx, "gen:go:std", "fs.write")
	if err != nil {
		t.Fatalf("ReadPolicy fs.write: %v", err)
	}
	if !found {
		t.Fatal("fs.write policy not found in Valkey")
	}
	if p1.NSamples != 10 {
		t.Errorf("fs.write NSamples = %d, want 10", p1.NSamples)
	}
	if p1.Alpha != 9 {
		t.Errorf("fs.write Alpha = %f, want 9", p1.Alpha)
	}
	if p1.Beta != 3 {
		t.Errorf("fs.write Beta = %f, want 3", p1.Beta)
	}

	// fs.read — Alpha = 10+1=11, Beta = 0+1=1
	p2, found, err := store.ReadPolicy(ctx, "gen:go:std", "fs.read")
	if err != nil {
		t.Fatalf("ReadPolicy fs.read: %v", err)
	}
	if !found {
		t.Fatal("fs.read policy not found in Valkey")
	}
	if p2.Alpha != 11 {
		t.Errorf("fs.read Alpha = %f, want 11", p2.Alpha)
	}
	if p2.ErrorRate != 0.0 {
		t.Errorf("fs.read ErrorRate = %f, want 0", p2.ErrorRate)
	}

	// git.commit — Alpha = 5+1=6, Beta = 5+1=6
	p3, found, err := store.ReadPolicy(ctx, "review:go:strict", "git.commit")
	if err != nil {
		t.Fatalf("ReadPolicy git.commit: %v", err)
	}
	if !found {
		t.Fatal("git.commit policy not found in Valkey")
	}
	if p3.Alpha != 6 || p3.Beta != 6 {
		t.Errorf("git.commit Alpha/Beta = %f/%f, want 6/6", p3.Alpha, p3.Beta)
	}

	// --- Verify audit: snapshot captured ---
	if len(audit.snapshots) != 1 {
		t.Fatalf("audit snapshots = %d, want 1", len(audit.snapshots))
	}
	if len(audit.snapshots[0].Policies) != 3 {
		t.Errorf("audit policies = %d, want 3", len(audit.snapshots[0].Policies))
	}

	// --- Verify NATS: event published ---
	select {
	case <-eventCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for NATS event")
	}

	eventMu.Lock()
	var evt map[string]interface{}
	if err := json.Unmarshal(eventReceived, &evt); err != nil {
		t.Fatalf("unmarshal NATS event: %v", err)
	}
	eventMu.Unlock()

	if evt["event"] != "tool_learning.policy.updated" {
		t.Errorf("event type = %v, want tool_learning.policy.updated", evt["event"])
	}
	if evt["schedule"] != "hourly" {
		t.Errorf("schedule = %v, want hourly", evt["schedule"])
	}
	if int(evt["policies_written"].(float64)) != 3 {
		t.Errorf("policies_written = %v, want 3", evt["policies_written"])
	}
}

// TestPipelineWithConstraints verifies hard SLO constraints filter out tools.
func TestPipelineWithConstraints(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb: %v", err)
	}
	defer db.Close()
	seedInvocations(t, db, now)
	lake, err := duckdb.NewLakeReader(db, "invocations")
	if err != nil {
		t.Fatalf("NewLakeReader: %v", err)
	}

	redisSrv := startMiniredis(t)
	store, err := valkey.NewPolicyStoreFromAddress(
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute,
	)
	if err != nil {
		t.Fatalf("valkey store: %v", err)
	}

	natsSrv := startNATS(t)
	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer conn.Close()
	pub := natspub.NewPublisher(conn, "hourly")

	audit := &captureAudit{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Max error rate 0.15 → fs.write (20%) and git.commit (50%) filtered
	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Constraints: domain.PolicyConstraints{
			MaxErrorRate: 0.15,
		},
		Clock:  fixedClock{t: now},
		Logger: logger,
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	if result.PoliciesWritten != 1 {
		t.Errorf("PoliciesWritten = %d, want 1", result.PoliciesWritten)
	}
	if result.PoliciesFiltered != 2 {
		t.Errorf("PoliciesFiltered = %d, want 2", result.PoliciesFiltered)
	}

	// Only fs.read should be in Valkey
	_, found, _ := store.ReadPolicy(context.Background(), "gen:go:std", "fs.read")
	if !found {
		t.Error("fs.read should be in Valkey")
	}
	_, found, _ = store.ReadPolicy(context.Background(), "gen:go:std", "fs.write")
	if found {
		t.Error("fs.write should NOT be in Valkey (filtered)")
	}
}

// TestPipelineEmptyLake verifies graceful handling when no data exists.
func TestPipelineEmptyLake(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE invocations (
		invocation_id VARCHAR, ts TIMESTAMP, tool_id VARCHAR,
		context_signature VARCHAR, outcome VARCHAR,
		latency_ms BIGINT, cost_units DOUBLE
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	lake, err := duckdb.NewLakeReader(db, "invocations")
	if err != nil {
		t.Fatalf("NewLakeReader: %v", err)
	}

	redisSrv := startMiniredis(t)
	store, err := valkey.NewPolicyStoreFromAddress(
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute,
	)
	if err != nil {
		t.Fatalf("valkey store: %v", err)
	}

	natsSrv := startNATS(t)
	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer conn.Close()
	pub := natspub.NewPublisher(conn, "hourly")

	audit := &captureAudit{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:      lake,
		Store:     store,
		Publisher: pub,
		Audit:     audit,
		Logger:    logger,
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly with empty lake: %v", err)
	}

	if result.AggregatesRead != 0 {
		t.Errorf("AggregatesRead = %d, want 0", result.AggregatesRead)
	}
	if result.PoliciesWritten != 0 {
		t.Errorf("PoliciesWritten = %d, want 0", result.PoliciesWritten)
	}
	if len(audit.snapshots) != 0 {
		t.Errorf("audit snapshots = %d, want 0", len(audit.snapshots))
	}
}
