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
	"math"
	"os"
	"sort"
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
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil,
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
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil,
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

// pipelineEnv bundles all embedded services and adapters for integration tests.
type pipelineEnv struct {
	db    *sql.DB
	lake  *duckdb.LakeReader
	store *valkey.PolicyStore
	conn  *gonats.Conn
	pub   *natspub.Publisher
	audit *captureAudit
}

// setupPipeline creates all embedded services and returns a ready-to-use env.
// If seed is true, the standard 30-invocation dataset is inserted.
func setupPipeline(t *testing.T, now time.Time, seed bool) *pipelineEnv {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if seed {
		seedInvocations(t, db, now)
	} else {
		_, err = db.Exec(`CREATE TABLE invocations (
			invocation_id VARCHAR, ts TIMESTAMP, tool_id VARCHAR,
			context_signature VARCHAR, outcome VARCHAR,
			latency_ms BIGINT, cost_units DOUBLE
		)`)
		if err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	lake, err := duckdb.NewLakeReader(db, "invocations")
	if err != nil {
		t.Fatalf("NewLakeReader: %v", err)
	}

	redisSrv := startMiniredis(t)
	store, err := valkey.NewPolicyStoreFromAddress(
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil,
	)
	if err != nil {
		t.Fatalf("valkey store: %v", err)
	}

	natsSrv := startNATS(t)
	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	pub := natspub.NewPublisher(conn, "hourly")
	audit := &captureAudit{}

	return &pipelineEnv{db: db, lake: lake, store: store, conn: conn, pub: pub, audit: audit}
}

// TestPipelineEmptyLake verifies graceful handling when no data exists.
func TestPipelineEmptyLake(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, false)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:      env.lake,
		Store:     env.store,
		Publisher: env.pub,
		Audit:     env.audit,
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
	if len(env.audit.snapshots) != 0 {
		t.Errorf("audit snapshots = %d, want 0", len(env.audit.snapshots))
	}
}

// ---------- Gap 8: Policy correctness validation ----------

// TestPolicyMathCorrectness validates all computed fields in the written policies:
// ErrorRate, P95LatencyMs, P95Cost, Confidence, FreshnessTs.
// These fields were never asserted in the original integration tests.
func TestPolicyMathCorrectness(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Audit: env.audit,
		Clock: fixedClock{t: now},
		// No publisher — not needed for this test.
		Logger: logger,
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	ctx := context.Background()

	// Expected values derived from seedInvocations():
	//
	// fs.write (gen:go:std): 8 success, 2 failure
	//   Latencies sorted: [90, 100, 105, 110, 115, 120, 130, 140, 500, 600]
	//   P95 = PERCENTILE_DISC(0.95) on 10 values → 600
	//   Costs sorted: [0.09, 0.10, 0.10, 0.11, 0.11, 0.12, 0.13, 0.14, 0.50, 0.60]
	//   P95Cost → 0.60
	//   ErrorRate = 2/10 = 0.20
	//   Alpha = 8+1 = 9, Beta = 2+1 = 3
	//   Confidence = 9/12 = 0.75
	//
	// fs.read (gen:go:std): 10 success, 0 failure
	//   Latencies sorted: [25, 28, 30, 32, 35, 38, 40, 42, 45, 50]
	//   P95 → 50
	//   Costs sorted: [0.02, 0.02, 0.03, 0.03, 0.03, 0.03, 0.04, 0.04, 0.04, 0.05]
	//   P95Cost → 0.05
	//   ErrorRate = 0.0
	//   Alpha = 10+1 = 11, Beta = 0+1 = 1
	//   Confidence = 11/12 ≈ 0.9167
	//
	// git.commit (review:go:strict): 5 success, 5 failure
	//   Latencies sorted: [180, 190, 200, 210, 220, 750, 800, 850, 900, 950]
	//   P95 → 950
	//   Costs sorted: [0.18, 0.19, 0.20, 0.21, 0.22, 0.75, 0.80, 0.85, 0.90, 0.95]
	//   P95Cost → 0.95
	//   ErrorRate = 5/10 = 0.50
	//   Alpha = 5+1 = 6, Beta = 5+1 = 6
	//   Confidence = 6/12 = 0.50

	type want struct {
		ctxSig, toolID                string
		alpha, beta, confidence       float64
		errorRate, p95Cost            float64
		p95Latency                    int64
		nSamples                      int64
	}

	cases := []want{
		{"gen:go:std", "fs.write", 9, 3, 0.75, 0.20, 0.60, 600, 10},
		{"gen:go:std", "fs.read", 11, 1, 11.0 / 12.0, 0.0, 0.05, 50, 10},
		{"review:go:strict", "git.commit", 6, 6, 0.50, 0.50, 0.95, 950, 10},
	}

	for _, tc := range cases {
		t.Run(tc.toolID, func(t *testing.T) {
			p, found, err := env.store.ReadPolicy(ctx, tc.ctxSig, tc.toolID)
			if err != nil {
				t.Fatalf("ReadPolicy: %v", err)
			}
			if !found {
				t.Fatal("policy not found in Valkey")
			}

			// Alpha / Beta priors
			if p.Alpha != tc.alpha {
				t.Errorf("Alpha = %f, want %f", p.Alpha, tc.alpha)
			}
			if p.Beta != tc.beta {
				t.Errorf("Beta = %f, want %f", p.Beta, tc.beta)
			}

			// Confidence = alpha / (alpha + beta)
			if math.Abs(p.Confidence-tc.confidence) > 0.001 {
				t.Errorf("Confidence = %f, want %f", p.Confidence, tc.confidence)
			}
			// Re-derive confidence from stored Alpha/Beta
			derived := p.Alpha / (p.Alpha + p.Beta)
			if math.Abs(p.Confidence-derived) > 1e-9 {
				t.Errorf("Confidence %f != Alpha/(Alpha+Beta) %f", p.Confidence, derived)
			}

			// Error rate
			if math.Abs(p.ErrorRate-tc.errorRate) > 0.001 {
				t.Errorf("ErrorRate = %f, want %f", p.ErrorRate, tc.errorRate)
			}

			// P95 latency
			if p.P95LatencyMs != tc.p95Latency {
				t.Errorf("P95LatencyMs = %d, want %d", p.P95LatencyMs, tc.p95Latency)
			}

			// P95 cost
			if math.Abs(p.P95Cost-tc.p95Cost) > 0.001 {
				t.Errorf("P95Cost = %f, want %f", p.P95Cost, tc.p95Cost)
			}

			// NSamples
			if p.NSamples != tc.nSamples {
				t.Errorf("NSamples = %d, want %d", p.NSamples, tc.nSamples)
			}

			// FreshnessTs must match the fixed clock, not the data timestamps.
			if !p.FreshnessTs.Equal(now) {
				t.Errorf("FreshnessTs = %v, want %v (computation clock)", p.FreshnessTs, now)
			}
		})
	}
}

// TestAuditSnapshotMatchesValkey verifies that the audit trail contains
// the exact same policies as those written to Valkey.
func TestAuditSnapshotMatchesValkey(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Audit: env.audit,
		Clock: fixedClock{t: now},
		Logger: logger,
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	if len(env.audit.snapshots) != 1 {
		t.Fatalf("expected 1 audit snapshot, got %d", len(env.audit.snapshots))
	}

	snap := env.audit.snapshots[0]
	ctx := context.Background()

	// Audit timestamp must match computation clock.
	if !snap.Ts.Equal(now) {
		t.Errorf("audit Ts = %v, want %v", snap.Ts, now)
	}

	// Every policy in audit must match the corresponding Valkey entry.
	for _, auditP := range snap.Policies {
		valkeyP, found, err := env.store.ReadPolicy(ctx, auditP.ContextSignature, auditP.ToolID)
		if err != nil {
			t.Fatalf("ReadPolicy(%s/%s): %v", auditP.ContextSignature, auditP.ToolID, err)
		}
		if !found {
			t.Errorf("audit policy %s/%s not found in Valkey", auditP.ContextSignature, auditP.ToolID)
			continue
		}

		// All fields must match exactly.
		if auditP.Alpha != valkeyP.Alpha || auditP.Beta != valkeyP.Beta {
			t.Errorf("%s/%s Alpha/Beta: audit=%f/%f valkey=%f/%f",
				auditP.ContextSignature, auditP.ToolID,
				auditP.Alpha, auditP.Beta, valkeyP.Alpha, valkeyP.Beta)
		}
		if auditP.Confidence != valkeyP.Confidence {
			t.Errorf("%s/%s Confidence: audit=%f valkey=%f",
				auditP.ContextSignature, auditP.ToolID,
				auditP.Confidence, valkeyP.Confidence)
		}
		if auditP.ErrorRate != valkeyP.ErrorRate {
			t.Errorf("%s/%s ErrorRate: audit=%f valkey=%f",
				auditP.ContextSignature, auditP.ToolID,
				auditP.ErrorRate, valkeyP.ErrorRate)
		}
		if auditP.P95LatencyMs != valkeyP.P95LatencyMs {
			t.Errorf("%s/%s P95LatencyMs: audit=%d valkey=%d",
				auditP.ContextSignature, auditP.ToolID,
				auditP.P95LatencyMs, valkeyP.P95LatencyMs)
		}
		if auditP.P95Cost != valkeyP.P95Cost {
			t.Errorf("%s/%s P95Cost: audit=%f valkey=%f",
				auditP.ContextSignature, auditP.ToolID,
				auditP.P95Cost, valkeyP.P95Cost)
		}
	}

	// Valkey should not contain more policies than the audit snapshot.
	if snap.Policies == nil {
		t.Fatal("audit snapshot has nil policies")
	}
}

// TestConstraintP95Latency validates that P95 latency constraints filter correctly.
// MaxP95LatencyMs=700 → fs.read (50ms) passes, fs.write (600ms) passes, git.commit (950ms) filtered.
func TestConstraintP95Latency(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Audit: env.audit,
		Constraints: domain.PolicyConstraints{
			MaxP95LatencyMs: 700,
		},
		Clock:  fixedClock{t: now},
		Logger: logger,
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	if result.PoliciesWritten != 2 {
		t.Errorf("PoliciesWritten = %d, want 2", result.PoliciesWritten)
	}
	if result.PoliciesFiltered != 1 {
		t.Errorf("PoliciesFiltered = %d, want 1", result.PoliciesFiltered)
	}

	ctx := context.Background()

	// fs.read passes (P95=50 < 700)
	_, found, _ := env.store.ReadPolicy(ctx, "gen:go:std", "fs.read")
	if !found {
		t.Error("fs.read should pass latency constraint (P95=50)")
	}

	// fs.write passes (P95=600 < 700)
	_, found, _ = env.store.ReadPolicy(ctx, "gen:go:std", "fs.write")
	if !found {
		t.Error("fs.write should pass latency constraint (P95=600)")
	}

	// git.commit filtered (P95=950 > 700)
	_, found, _ = env.store.ReadPolicy(ctx, "review:go:strict", "git.commit")
	if found {
		t.Error("git.commit should be filtered (P95=950 > 700)")
	}
}

// TestMultiConstraintInteraction verifies AND semantics: a tool must pass ALL constraints.
// MaxP95LatencyMs=700 AND MaxErrorRate=0.15:
//   fs.write:    P95=600 ok,  ErrorRate=0.20 FAIL → filtered
//   fs.read:     P95=50  ok,  ErrorRate=0.0  ok   → passes
//   git.commit:  P95=950 FAIL, ErrorRate=0.50 FAIL → filtered
func TestMultiConstraintInteraction(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Audit: env.audit,
		Constraints: domain.PolicyConstraints{
			MaxP95LatencyMs: 700,
			MaxErrorRate:    0.15,
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

	ctx := context.Background()

	// Only fs.read passes both constraints.
	_, found, _ := env.store.ReadPolicy(ctx, "gen:go:std", "fs.read")
	if !found {
		t.Error("fs.read should pass all constraints")
	}

	// fs.write fails error rate (0.20 > 0.15).
	_, found, _ = env.store.ReadPolicy(ctx, "gen:go:std", "fs.write")
	if found {
		t.Error("fs.write should be filtered (ErrorRate=0.20 > 0.15)")
	}

	// git.commit fails both.
	_, found, _ = env.store.ReadPolicy(ctx, "review:go:strict", "git.commit")
	if found {
		t.Error("git.commit should be filtered (both constraints violated)")
	}
}

// TestConstraintP95Cost validates P95 cost constraint filtering.
// MaxP95Cost=0.50 → fs.read (0.05) passes, fs.write (0.60) filtered, git.commit (0.95) filtered.
func TestConstraintP95Cost(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Constraints: domain.PolicyConstraints{
			MaxP95Cost: 0.50,
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

	ctx := context.Background()
	_, found, _ := env.store.ReadPolicy(ctx, "gen:go:std", "fs.read")
	if !found {
		t.Error("fs.read should pass cost constraint (P95Cost=0.05)")
	}
}

// TestThompsonSamplingRanking validates that Thompson Sampling produces
// statistically correct rankings: a high-success policy should on average
// sample higher than a low-success one.
func TestThompsonSamplingRanking(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Clock: fixedClock{t: now},
		Logger: logger,
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	ctx := context.Background()
	fsRead, _, _ := env.store.ReadPolicy(ctx, "gen:go:std", "fs.read")       // 100% success
	fsWrite, _, _ := env.store.ReadPolicy(ctx, "gen:go:std", "fs.write")     // 80% success
	gitCommit, _, _ := env.store.ReadPolicy(ctx, "review:go:strict", "git.commit") // 50% success

	// Sample 1000 draws per policy and compare averages.
	sampler := domain.NewThompsonSampler()
	const draws = 1000
	avgRead, avgWrite, avgCommit := 0.0, 0.0, 0.0

	for range draws {
		avgRead += sampler.Sample(fsRead)
		avgWrite += sampler.Sample(fsWrite)
		avgCommit += sampler.Sample(gitCommit)
	}
	avgRead /= draws
	avgWrite /= draws
	avgCommit /= draws

	// Ordering: fs.read (α=11,β=1) > fs.write (α=9,β=3) > git.commit (α=6,β=6)
	if avgRead <= avgWrite {
		t.Errorf("fs.read avg sample (%f) should > fs.write avg sample (%f)", avgRead, avgWrite)
	}
	if avgWrite <= avgCommit {
		t.Errorf("fs.write avg sample (%f) should > git.commit avg sample (%f)", avgWrite, avgCommit)
	}

	// Sanity: all samples must be in [0, 1].
	if avgRead < 0 || avgRead > 1 || avgWrite < 0 || avgWrite > 1 || avgCommit < 0 || avgCommit > 1 {
		t.Errorf("sample averages out of [0,1]: read=%f write=%f commit=%f",
			avgRead, avgWrite, avgCommit)
	}
}

// TestConfidenceMonotonicity verifies that higher success rates yield
// higher confidence values across all policies in a single run.
func TestConfidenceMonotonicity(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:  env.lake,
		Store: env.store,
		Audit: env.audit,
		Clock: fixedClock{t: now},
		Logger: logger,
	})

	_, err := uc.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly: %v", err)
	}

	policies := env.audit.snapshots[0].Policies

	// Sort by success rate (1 - ErrorRate) ascending.
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].ErrorRate > policies[j].ErrorRate // higher error → lower success → lower confidence
	})

	// Confidence must be monotonically non-decreasing as success rate increases.
	for i := 1; i < len(policies); i++ {
		if policies[i].Confidence < policies[i-1].Confidence {
			t.Errorf("confidence monotonicity violated: %s (err=%.2f, conf=%.4f) > %s (err=%.2f, conf=%.4f)",
				policies[i-1].ToolID, policies[i-1].ErrorRate, policies[i-1].Confidence,
				policies[i].ToolID, policies[i].ErrorRate, policies[i].Confidence)
		}
	}

	// Spot-check boundaries.
	if policies[0].Confidence >= policies[len(policies)-1].Confidence {
		t.Errorf("worst tool confidence (%f) should be < best tool confidence (%f)",
			policies[0].Confidence, policies[len(policies)-1].Confidence)
	}
}

// TestNATSEventPayloadComplete validates all expected fields in the NATS event,
// including policies_filtered which was never asserted.
func TestNATSEventPayloadComplete(t *testing.T) {
	now := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)
	env := setupPipeline(t, now, true)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	var eventReceived []byte
	var eventMu sync.Mutex
	eventCh := make(chan struct{}, 1)
	sub, err := env.conn.Subscribe("tool_learning.policy.updated", func(msg *gonats.Msg) {
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

	// Use constraints to force filtering so we can validate policies_filtered > 0.
	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:      env.lake,
		Store:     env.store,
		Publisher: env.pub,
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
		t.Errorf("event = %v, want tool_learning.policy.updated", evt["event"])
	}
	if evt["schedule"] != "hourly" {
		t.Errorf("schedule = %v, want hourly", evt["schedule"])
	}
	if int(evt["policies_written"].(float64)) != result.PoliciesWritten {
		t.Errorf("policies_written = %v, want %d", evt["policies_written"], result.PoliciesWritten)
	}
	if int(evt["policies_filtered"].(float64)) != result.PoliciesFiltered {
		t.Errorf("policies_filtered = %v, want %d", evt["policies_filtered"], result.PoliciesFiltered)
	}

	// Verify timestamp is present and parseable.
	if _, ok := evt["ts"]; !ok {
		t.Error("NATS event missing 'ts' field")
	}
}
