// demo runs the full tool-learning pipeline with embedded services and
// produces formatted terminal output showing Thompson Sampling in action.
//
// Zero infrastructure required — DuckDB, Valkey, and NATS all run in-memory.
//
// Usage:
//
//	go run ./cmd/demo
//	go run ./cmd/demo --hours=6 --per-hour=500 --max-error-rate=0.08
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// ANSI color codes for terminal formatting.
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	white  = "\033[37m"
	bgBlue = "\033[44m"
)

func main() {
	hours := flag.Int("hours", 24, "Hours of synthetic telemetry to generate")
	perHour := flag.Int("per-hour", 200, "Invocations per hour per tool combo")
	maxLatency := flag.Int64("max-p95-latency-ms", 0, "Hard constraint: max P95 latency (0=disabled)")
	maxErrorRate := flag.Float64("max-error-rate", 0, "Hard constraint: max error rate (0=disabled)")
	maxCost := flag.Float64("max-p95-cost", 0, "Hard constraint: max P95 cost (0=disabled)")
	samplingRounds := flag.Int("sampling-rounds", 5, "Thompson Sampling demo rounds")
	flag.Parse()

	if err := runDemo(demoConfig{
		hours:          *hours,
		perHour:        *perHour,
		maxLatency:     *maxLatency,
		maxErrorRate:   *maxErrorRate,
		maxCost:        *maxCost,
		samplingRounds: *samplingRounds,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "%serror: %v%s\n", red, err, reset)
		os.Exit(1)
	}
}

type demoConfig struct {
	hours, perHour int
	maxLatency     int64
	maxErrorRate   float64
	maxCost        float64
	samplingRounds int
}

func runDemo(cfg demoConfig) error {
	printBanner()

	// --- Phase 1: Seed telemetry lake ---
	printPhase(1, "Seeding Telemetry Lake")
	db, invocations, combos, err := seedLake(cfg.hours, cfg.perHour)
	if err != nil {
		return fmt.Errorf("seed lake: %w", err)
	}
	defer func() { _ = db.Close() }()

	fmt.Printf("  DuckDB in-memory    %s(no infrastructure needed)%s\n", dim, reset)
	fmt.Printf("  Hours generated     %s%d%s\n", bold, cfg.hours, reset)
	fmt.Printf("  Per hour / combo    %s%d%s\n", bold, cfg.perHour, reset)
	fmt.Printf("  Tool/context combos %s%d%s\n", bold, combos, reset)
	fmt.Printf("  Total invocations   %s%s%s\n\n", bold+cyan, formatNumber(invocations), reset)

	// --- Phase 2: Start embedded services ---
	printPhase(2, "Starting Embedded Services")
	lake, store, pub, conn, audit, cleanup, err := startServices(db)
	if err != nil {
		return fmt.Errorf("start services: %w", err)
	}
	defer cleanup()

	// Subscribe to NATS event before running pipeline.
	var natsEvent []byte
	var natsMu sync.Mutex
	eventCh := make(chan struct{}, 1)
	sub, err := conn.Subscribe("tool_learning.policy.updated", func(msg *gonats.Msg) {
		natsMu.Lock()
		natsEvent = msg.Data
		natsMu.Unlock()
		select {
		case eventCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	fmt.Printf("  Valkey (miniredis)  %s[ready]%s\n", green, reset)
	fmt.Printf("  NATS (embedded)     %s[ready]%s\n", green, reset)
	fmt.Printf("  Audit (in-memory)   %s[ready]%s\n\n", green, reset)

	// --- Phase 3: Run pipeline ---
	printPhase(3, "Computing Policies — Thompson Sampling")
	constraints := domain.PolicyConstraints{
		MaxP95LatencyMs: cfg.maxLatency,
		MaxErrorRate:    cfg.maxErrorRate,
		MaxP95Cost:      cfg.maxCost,
	}

	start := time.Now()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:        lake,
		Store:       store,
		Publisher:   pub,
		Audit:       audit,
		Constraints: constraints,
		Logger:      logger,
	})

	result, err := uc.RunHourly(context.Background())
	if err != nil {
		return fmt.Errorf("pipeline: %w", err)
	}
	elapsed := time.Since(start)

	printConstraints(constraints)
	fmt.Printf("\n  Aggregates read     %s%d%s\n", bold, result.AggregatesRead, reset)
	fmt.Printf("  Policies computed   %s%d%s\n", bold, result.AggregatesRead, reset)
	fmt.Printf("  Policies written    %s%s%d%s\n", bold, green, result.PoliciesWritten, reset)
	if result.PoliciesFiltered > 0 {
		fmt.Printf("  Policies filtered   %s%s%d%s\n", bold, red, result.PoliciesFiltered, reset)
	} else {
		fmt.Printf("  Policies filtered   %s0%s\n", dim, reset)
	}
	fmt.Printf("  Duration            %s%s%s\n\n", bold, elapsed.Round(time.Millisecond), reset)

	// --- Phase 4: Policy table ---
	printPhase(4, "Computed Policies — Ranked by Confidence")
	policies, err := readAllPolicies(store, audit)
	if err != nil {
		return fmt.Errorf("read policies: %w", err)
	}
	printPolicyTable(policies, constraints)

	// --- Phase 5: Thompson Sampling live draws ---
	printPhase(5, "Thompson Sampling — Live Draws")
	fmt.Printf("  Each round samples from Beta(alpha, beta) for every policy.\n")
	fmt.Printf("  %sNotice how rankings shift — this is exploration vs exploitation.%s\n\n", dim, reset)
	printSamplingRounds(policies, cfg.samplingRounds)

	// --- Phase 6: NATS event ---
	printPhase(6, "NATS Event Published")
	select {
	case <-eventCh:
		natsMu.Lock()
		printNATSEvent(natsEvent)
		natsMu.Unlock()
	case <-time.After(2 * time.Second):
		fmt.Printf("  %s(timeout waiting for event)%s\n", yellow, reset)
	}

	// --- Phase 7: Audit snapshot ---
	printPhase(7, "Audit Snapshot Captured")
	snap := audit.Snapshots()
	if len(snap) > 0 {
		fmt.Printf("  Timestamp    %s%s%s\n", bold, snap[0].Ts.Format(time.RFC3339), reset)
		fmt.Printf("  Policies     %s%d%s\n", bold, len(snap[0].Policies), reset)
		fmt.Printf("  S3 path      %saudit/dt=%s/hour=%s/snapshot-%s.json%s\n",
			dim,
			snap[0].Ts.Format("2006-01-02"),
			snap[0].Ts.Format("15"),
			snap[0].Ts.Format("20060102T150405Z"),
			reset,
		)
	}

	printFooter(invocations, result, elapsed)
	return nil
}

// ---------------------------------------------------------------------------
// Telemetry lake seeding
// ---------------------------------------------------------------------------

func seedLake(hours, perHour int) (*sql.DB, int64, int, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, 0, 0, err
	}

	minuteSpan := strconv.Itoa(hours * 60)
	totalRows := strconv.Itoa(hours * perHour)
	genSQL := `
CREATE TABLE invocations AS
SELECT
    'inv-' || gen_random_uuid()::VARCHAR AS invocation_id,
    ts,
    tool_id, context_signature,
    CASE WHEN random() < error_rate THEN 'failure' ELSE 'success' END AS outcome,
    CAST(base_latency + abs(hash(gen_random_uuid())) % variance AS BIGINT) AS latency_ms,
    ROUND(base_cost + random() * cost_variance, 4) AS cost_units
FROM (
    SELECT
        now()::TIMESTAMP - INTERVAL (abs(hash(gen_random_uuid())) % ` + minuteSpan + `) MINUTE AS ts,
        tool_id, context_signature, error_rate,
        base_latency, variance, base_cost, cost_variance
    FROM generate_series(1, ` + totalRows + `) AS _(i)
    CROSS JOIN (VALUES
        ('fs.write',     'gen:go:std',       0.05,  80,  120, 0.08, 0.04),
        ('fs.read',      'gen:go:std',       0.02,  30,   40, 0.03, 0.02),
        ('fs.search',    'gen:go:std',       0.03, 120,  200, 0.12, 0.06),
        ('git.status',   'gen:go:std',       0.01,  50,   60, 0.05, 0.02),
        ('git.diff',     'gen:go:std',       0.02,  70,  100, 0.06, 0.03),
        ('git.commit',   'gen:go:std',       0.04, 150,  300, 0.15, 0.08),
        ('repo.build',   'gen:go:std',       0.08, 500, 2000, 0.50, 0.30),
        ('repo.test',    'gen:go:std',       0.10, 800, 3000, 0.80, 0.40),
        ('fs.write',     'gen:python:std',   0.06,  90,  130, 0.09, 0.05),
        ('fs.read',      'gen:python:std',   0.02,  35,   45, 0.04, 0.02),
        ('repo.build',   'gen:python:std',   0.07, 400, 1500, 0.40, 0.25),
        ('repo.test',    'gen:python:std',   0.12, 600, 2500, 0.60, 0.35),
        ('fs.write',     'review:go:strict', 0.03,  70,  100, 0.07, 0.03),
        ('fs.read',      'review:go:strict', 0.01,  25,   35, 0.03, 0.01),
        ('git.commit',   'review:go:strict', 0.02, 130,  250, 0.13, 0.06),
        ('security.scan','gen:go:std',       0.05, 200,  500, 0.20, 0.10)
    ) AS tools(tool_id, context_signature, error_rate, base_latency, variance, base_cost, cost_variance)
)
`
	if _, err := db.Exec(genSQL); err != nil {
		_ = db.Close()
		return nil, 0, 0, fmt.Errorf("generate: %w", err)
	}

	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM invocations").Scan(&count); err != nil {
		_ = db.Close()
		return nil, 0, 0, err
	}

	var combos int
	if err := db.QueryRow("SELECT COUNT(DISTINCT tool_id || ':' || context_signature) FROM invocations").Scan(&combos); err != nil {
		_ = db.Close()
		return nil, 0, 0, err
	}

	return db, count, combos, nil
}

// ---------------------------------------------------------------------------
// Embedded services
// ---------------------------------------------------------------------------

// captureAudit records snapshots in-memory.
type captureAudit struct {
	mu    sync.Mutex
	snaps []auditSnap
}

type auditSnap struct {
	Ts       time.Time
	Policies []domain.ToolPolicy
}

func (c *captureAudit) WriteSnapshot(_ context.Context, ts time.Time, policies []domain.ToolPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]domain.ToolPolicy, len(policies))
	copy(cp, policies)
	c.snaps = append(c.snaps, auditSnap{Ts: ts, Policies: cp})
	return nil
}

func (c *captureAudit) Snapshots() []auditSnap {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snaps
}

func startServices(db *sql.DB) (
	*duckdb.LakeReader,
	*valkey.PolicyStore,
	*natspub.Publisher,
	*gonats.Conn,
	*captureAudit,
	func(),
	error,
) {
	lake, err := duckdb.NewLakeReader(db, "invocations")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	redisSrv, err := miniredis.Run()
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	store, err := valkey.NewPolicyStoreFromAddress(
		context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 2*time.Hour, nil,
	)
	if err != nil {
		redisSrv.Close()
		return nil, nil, nil, nil, nil, nil, err
	}

	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1}
	natsSrv, err := natsserver.NewServer(opts)
	if err != nil {
		redisSrv.Close()
		return nil, nil, nil, nil, nil, nil, err
	}
	go natsSrv.Start()
	if !natsSrv.ReadyForConnections(5 * time.Second) {
		redisSrv.Close()
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("nats not ready")
	}

	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		natsSrv.Shutdown()
		redisSrv.Close()
		return nil, nil, nil, nil, nil, nil, err
	}

	pub := natspub.NewPublisher(conn, "hourly")
	audit := &captureAudit{}

	cleanup := func() {
		conn.Close()
		natsSrv.Shutdown()
		redisSrv.Close()
	}

	return lake, store, pub, conn, audit, cleanup, nil
}

// ---------------------------------------------------------------------------
// Read policies from audit snapshot
// ---------------------------------------------------------------------------

func readAllPolicies(_ *valkey.PolicyStore, audit *captureAudit) ([]domain.ToolPolicy, error) {
	snaps := audit.Snapshots()
	if len(snaps) == 0 {
		return nil, fmt.Errorf("no audit snapshots")
	}
	// Return sorted by confidence descending.
	policies := make([]domain.ToolPolicy, len(snaps[0].Policies))
	copy(policies, snaps[0].Policies)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Confidence > policies[j].Confidence
	})
	return policies, nil
}

// ---------------------------------------------------------------------------
// Terminal output
// ---------------------------------------------------------------------------

func printBanner() {
	fmt.Println()
	fmt.Printf("  %s%s                                                          %s\n", bold, bgBlue, reset)
	fmt.Printf("  %s%s   TOOL LEARNING  --  Thompson Sampling Policy Pipeline   %s\n", bold+white, bgBlue, reset)
	fmt.Printf("  %s%s                                                          %s\n", bold, bgBlue, reset)
	fmt.Println()
	fmt.Printf("  %sZero infrastructure demo — DuckDB + Valkey + NATS all in-memory%s\n\n", dim, reset)
}

func printPhase(n int, title string) {
	fmt.Printf("  %s%s--- [%d] %s ---%s\n\n", bold, cyan, n, title, reset)
}

func printConstraints(c domain.PolicyConstraints) {
	if c.MaxP95LatencyMs == 0 && c.MaxErrorRate == 0 && c.MaxP95Cost == 0 {
		fmt.Printf("  Constraints         %s(none — all policies pass)%s\n", dim, reset)
		return
	}
	parts := []string{}
	if c.MaxP95LatencyMs > 0 {
		parts = append(parts, fmt.Sprintf("P95 latency <= %dms", c.MaxP95LatencyMs))
	}
	if c.MaxErrorRate > 0 {
		parts = append(parts, fmt.Sprintf("error rate <= %.1f%%", c.MaxErrorRate*100))
	}
	if c.MaxP95Cost > 0 {
		parts = append(parts, fmt.Sprintf("P95 cost <= %.2f", c.MaxP95Cost))
	}
	fmt.Printf("  Constraints         %s%s%s\n", yellow, strings.Join(parts, " AND "), reset)
}

func printPolicyTable(policies []domain.ToolPolicy, constraints domain.PolicyConstraints) {
	header := fmt.Sprintf("  %s%-3s %-16s %-18s %7s %8s %8s %10s %8s %8s%s",
		bold, "#", "Tool", "Context", "Samples", "Alpha", "Beta", "Confidence", "ErrRate", "P95ms", reset)
	sep := "  " + strings.Repeat("-", 102)

	fmt.Println(header)
	fmt.Println(sep)

	for i, p := range policies {
		conf := fmt.Sprintf("%.4f", p.Confidence)
		errRate := fmt.Sprintf("%.2f%%", p.ErrorRate*100)
		p95 := fmt.Sprintf("%d", p.P95LatencyMs)

		// Color confidence: green > 0.95, yellow > 0.85, red otherwise.
		confColor := red
		if p.Confidence >= 0.95 {
			confColor = green
		} else if p.Confidence >= 0.85 {
			confColor = yellow
		}

		// Highlight filtered tools.
		filtered := !constraints.IsEligible(p)
		prefix := " "
		if filtered {
			prefix = red + "x" + reset
		}

		fmt.Printf("  %s%-3d %-16s %-18s %7d %8.0f %8.0f %s%10s%s %8s %8s\n",
			prefix, i+1, p.ToolID, p.ContextSignature,
			p.NSamples, p.Alpha, p.Beta,
			confColor, conf, reset,
			errRate, p95)
	}
	fmt.Println(sep)
	fmt.Println()
}

func printSamplingRounds(policies []domain.ToolPolicy, rounds int) {
	if len(policies) == 0 {
		return
	}
	sampler := domain.NewThompsonSampler()

	type scored struct {
		tool  string
		score float64
	}

	for round := 1; round <= rounds; round++ {
		scores := make([]scored, len(policies))
		for i, p := range policies {
			scores[i] = scored{
				tool:  p.ToolID,
				score: sampler.Sample(p),
			}
		}
		sort.Slice(scores, func(i, j int) bool {
			return scores[i].score > scores[j].score
		})

		fmt.Printf("  %sRound %d:%s ", dim, round, reset)
		for i, s := range scores {
			if i >= 5 {
				fmt.Printf("%s...%s", dim, reset)
				break
			}
			color := green
			if i == 0 {
				color = bold + green
			} else if i >= 3 {
				color = dim
			}
			if i > 0 {
				fmt.Print("  >  ")
			}
			fmt.Printf("%s%s (%.3f)%s", color, s.tool, s.score, reset)
		}
		fmt.Println()
	}
	fmt.Println()
}

func printNATSEvent(data []byte) {
	var evt map[string]any
	if err := json.Unmarshal(data, &evt); err != nil {
		fmt.Printf("  %s(parse error: %v)%s\n", red, err, reset)
		return
	}
	fmt.Printf("  Subject   %stool_learning.policy.updated%s\n", bold, reset)
	for _, key := range []string{"event", "ts", "schedule", "policies_written", "policies_filtered"} {
		if v, ok := evt[key]; ok {
			switch val := v.(type) {
			case float64:
				fmt.Printf("  %-11s%s%d%s\n", key, bold, int(val), reset)
			default:
				fmt.Printf("  %-11s%s%v%s\n", key, bold, val, reset)
			}
		}
	}
	fmt.Println()
}

func printFooter(invocations int64, result app.ComputeResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Printf("  %s%s                                                          %s\n", bold, bgBlue, reset)
	fmt.Printf("  %s%s   SUMMARY                                                %s\n", bold+white, bgBlue, reset)
	fmt.Printf("  %s%s                                                          %s\n", bold, bgBlue, reset)
	fmt.Println()
	fmt.Printf("  %s invocations processed  %s%s%s\n", formatNumber(invocations), bold, green, reset)
	fmt.Printf("  %d aggregates computed     in %s\n", result.AggregatesRead, elapsed.Round(time.Millisecond))
	fmt.Printf("  %d policies persisted to Valkey\n", result.PoliciesWritten)
	if result.PoliciesFiltered > 0 {
		fmt.Printf("  %d policies filtered by constraints\n", result.PoliciesFiltered)
	}
	fmt.Printf("  1 audit snapshot written\n")
	fmt.Printf("  1 NATS event published\n")
	fmt.Println()
	fmt.Printf("  %sBayesian Thompson Sampling provides optimal explore/exploit trade-off%s\n", dim, reset)
	fmt.Printf("  %sfor tool selection ranking across context signatures.%s\n\n", dim, reset)
}

func formatNumber(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
