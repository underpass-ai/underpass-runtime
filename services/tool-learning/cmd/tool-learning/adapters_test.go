package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/marcboeker/go-duckdb"
	server "github.com/nats-io/nats-server/v2/server"
	gonats "github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/duckdb"
	natspub "github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/nats"
	s3store "github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/s3"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/valkey"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func startMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	srv := miniredis.RunT(t)
	return srv
}

func startTestNATS(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Host: "127.0.0.1", Port: -1}
	srv, err := server.NewServer(opts)
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

// buildTestAdapters creates all adapters using in-memory/embedded services.
func buildTestAdapters(t *testing.T) (
	*duckdb.LakeReader,
	*valkey.PolicyStore,
	*natspub.Publisher,
	*s3store.AuditStore,
	func(),
) {
	t.Helper()

	// DuckDB in-memory with empty invocations table
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb: %v", err)
	}
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

	// Valkey via miniredis
	redisSrv := startMiniredis(t)
	store, err := valkey.NewPolicyStoreFromAddress(context.Background(), redisSrv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil)
	if err != nil {
		t.Fatalf("valkey: %v", err)
	}

	// NATS via embedded server
	natsSrv := startTestNATS(t)
	conn, err := gonats.Connect(natsSrv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	pub := natspub.NewPublisher(conn, "hourly")

	// S3 audit store (client creation doesn't connect)
	audit, err := s3store.NewAuditStoreFromConfig("localhost:9000", "test", "test", "test-audit", false, nil)
	if err != nil {
		t.Fatalf("audit store: %v", err)
	}

	cleanup := func() {
		_ = lake.Close()
		pub.Close()
		conn.Close()
	}
	return lake, store, pub, audit, cleanup
}

func TestBuildAdapters(t *testing.T) {
	redis := startMiniredis(t)
	nats := startTestNATS(t)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := adapterConfig{
		S3Endpoint:  "localhost:9000",
		S3AccessKey: "test",
		S3SecretKey: "test",
		S3Region:    "us-east-1",
		S3UseSSL:    false,
		LakeBucket:  "test-lake",
		AuditBucket: "test-audit",
		ValkeyAddr:  redis.Addr(),
		ValkeyPass:  "",
		ValkeyDB:    0,
		ValkeyPfx:   "tool_policy",
		ValkeyTTL:   10 * time.Minute,
		NATSURL:     nats.ClientURL(),
		Schedule:    "hourly",
	}

	lake, store, _, pub, audit, cleanup, err := buildAdapters(cfg, logger)
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	defer cleanup()

	if lake == nil {
		t.Error("lake is nil")
	}
	if store == nil {
		t.Error("store is nil")
	}
	if pub == nil {
		t.Error("publisher is nil")
	}
	if audit == nil {
		t.Error("audit is nil")
	}
}

func TestBuildAdaptersValkeyFailure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := adapterConfig{
		S3Endpoint:  "localhost:9000",
		S3AccessKey: "test",
		S3SecretKey: "test",
		S3Region:    "us-east-1",
		LakeBucket:  "test-lake",
		AuditBucket: "test-audit",
		ValkeyAddr:  "localhost:1",
		ValkeyPfx:   "tool_policy",
		ValkeyTTL:   10 * time.Minute,
		NATSURL:     "nats://localhost:4222",
		Schedule:    "hourly",
	}

	_, _, _, _, _, _, err := buildAdapters(cfg, logger)
	if err == nil {
		t.Fatal("expected error from invalid Valkey address")
	}
}

func TestBuildAdaptersNATSFailure(t *testing.T) {
	redis := startMiniredis(t)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := adapterConfig{
		S3Endpoint:  "localhost:9000",
		S3AccessKey: "test",
		S3SecretKey: "test",
		S3Region:    "us-east-1",
		LakeBucket:  "test-lake",
		AuditBucket: "test-audit",
		ValkeyAddr:  redis.Addr(),
		ValkeyPfx:   "tool_policy",
		ValkeyTTL:   10 * time.Minute,
		NATSURL:     "nats://invalid:9999",
		Schedule:    "hourly",
	}

	_, _, _, _, _, _, err := buildAdapters(cfg, logger)
	if err == nil {
		t.Fatal("expected error from invalid NATS URL")
	}
}

func TestExecuteHourly(t *testing.T) {
	lake, store, pub, audit, cleanup := buildTestAdapters(t)
	defer cleanup()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	err := execute(context.Background(), executeParams{
		Lake: lake, Store: store, Publisher: pub, Audit: audit,
		Constraints: domain.PolicyConstraints{}, Schedule: "hourly", Logger: logger,
	})
	if err != nil {
		t.Fatalf("execute hourly: %v", err)
	}
}

func TestExecuteDaily(t *testing.T) {
	lake, store, pub, audit, cleanup := buildTestAdapters(t)
	defer cleanup()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	err := execute(context.Background(), executeParams{
		Lake: lake, Store: store, Publisher: pub, Audit: audit,
		Constraints: domain.PolicyConstraints{}, Schedule: "daily", Logger: logger,
	})
	if err != nil {
		t.Fatalf("execute daily: %v", err)
	}
}

func TestExecuteUnknownSchedule(t *testing.T) {
	lake, store, pub, audit, cleanup := buildTestAdapters(t)
	defer cleanup()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	err := execute(context.Background(), executeParams{
		Lake: lake, Store: store, Publisher: pub, Audit: audit,
		Constraints: domain.PolicyConstraints{}, Schedule: "weekly", Logger: logger,
	})
	if err == nil {
		t.Fatal("expected error for unknown schedule")
	}
}
