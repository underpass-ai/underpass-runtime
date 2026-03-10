package duckdb

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE invocations (
		invocation_id VARCHAR,
		ts TIMESTAMP,
		tool_id VARCHAR,
		context_signature VARCHAR,
		outcome VARCHAR,
		latency_ms BIGINT,
		cost_units DOUBLE
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestQueryAggregatesEmpty(t *testing.T) {
	db := openTestDB(t)
	reader := NewLakeReader(db, "invocations")

	from := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	results, err := reader.QueryAggregates(context.Background(), from, to)
	if err != nil {
		t.Fatalf("QueryAggregates: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQueryAggregates(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Exec(`INSERT INTO invocations VALUES
		('inv1', '2026-03-09 12:00:00', 'fs.write', 'gen:go:std', 'success', 100, 0.1),
		('inv2', '2026-03-09 12:01:00', 'fs.write', 'gen:go:std', 'success', 200, 0.2),
		('inv3', '2026-03-09 12:02:00', 'fs.write', 'gen:go:std', 'success', 150, 0.15),
		('inv4', '2026-03-09 12:03:00', 'fs.write', 'gen:go:std', 'failure', 500, 0.5),
		('inv5', '2026-03-09 12:00:00', 'fs.read', 'gen:go:std', 'success', 50, 0.05),
		('inv6', '2026-03-09 12:01:00', 'fs.read', 'gen:go:std', 'success', 60, 0.06)
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	reader := NewLakeReader(db, "invocations")
	from := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	results, err := reader.QueryAggregates(context.Background(), from, to)
	if err != nil {
		t.Fatalf("QueryAggregates: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify fs.write aggregates
	byTool := map[string]int{}
	for i, r := range results {
		byTool[r.ToolID] = i
	}

	idx, ok := byTool["fs.write"]
	if !ok {
		t.Fatal("fs.write not found in results")
	}
	fw := results[idx]
	if fw.Total != 4 {
		t.Errorf("fs.write total = %d, want 4", fw.Total)
	}
	if fw.Successes != 3 {
		t.Errorf("fs.write successes = %d, want 3", fw.Successes)
	}
	if fw.Failures != 1 {
		t.Errorf("fs.write failures = %d, want 1", fw.Failures)
	}
	if fw.ErrorRate < 0.24 || fw.ErrorRate > 0.26 {
		t.Errorf("fs.write error_rate = %f, want ~0.25", fw.ErrorRate)
	}

	// Verify fs.read aggregates
	idx, ok = byTool["fs.read"]
	if !ok {
		t.Fatal("fs.read not found in results")
	}
	fr := results[idx]
	if fr.Total != 2 {
		t.Errorf("fs.read total = %d, want 2", fr.Total)
	}
	if fr.Successes != 2 {
		t.Errorf("fs.read successes = %d, want 2", fr.Successes)
	}
	if fr.ErrorRate != 0.0 {
		t.Errorf("fs.read error_rate = %f, want 0", fr.ErrorRate)
	}
}

func TestQueryAggregatesTimeFilter(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Exec(`INSERT INTO invocations VALUES
		('inv1', '2026-03-09 11:00:00', 'fs.write', 'gen:go:std', 'success', 100, 0.1),
		('inv2', '2026-03-09 12:30:00', 'fs.write', 'gen:go:std', 'success', 200, 0.2),
		('inv3', '2026-03-09 13:00:00', 'fs.write', 'gen:go:std', 'failure', 500, 0.5)
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	reader := NewLakeReader(db, "invocations")

	// Only query 12:00-13:00 window — should get 1 record
	from := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 9, 13, 0, 0, 0, time.UTC)

	results, err := reader.QueryAggregates(context.Background(), from, to)
	if err != nil {
		t.Fatalf("QueryAggregates: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Total != 1 {
		t.Errorf("total = %d, want 1", results[0].Total)
	}
}

func TestNewLakeReaderFromS3(t *testing.T) {
	// DuckDB can configure S3 settings without a real endpoint.
	// The connection and SET commands succeed in-memory.
	reader, err := NewLakeReaderFromS3("localhost:9000", "access", "secret", "test-bucket", "us-east-1", false)
	if err != nil {
		t.Fatalf("NewLakeReaderFromS3: %v", err)
	}
	defer reader.Close()

	if reader.source == "" {
		t.Error("expected non-empty source")
	}
}

func TestNewLakeReaderFromS3WithSSL(t *testing.T) {
	reader, err := NewLakeReaderFromS3("s3.amazonaws.com", "access", "secret", "bucket", "us-west-2", true)
	if err != nil {
		t.Fatalf("NewLakeReaderFromS3 with SSL: %v", err)
	}
	defer reader.Close()
}

func TestLakeReaderClose(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	reader := NewLakeReader(db, "invocations")
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestQueryAggregatesP95(t *testing.T) {
	db := openTestDB(t)

	// Insert 20 records to make p95 meaningful
	_, err := db.Exec(`INSERT INTO invocations
		SELECT
			'inv' || i,
			'2026-03-09 12:00:00'::TIMESTAMP + INTERVAL (i) MINUTE,
			'fs.write',
			'gen:go:std',
			CASE WHEN i <= 18 THEN 'success' ELSE 'failure' END,
			100 + i * 10,
			0.1 + i * 0.01
		FROM generate_series(1, 20) AS t(i)
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	reader := NewLakeReader(db, "invocations")
	from := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	results, err := reader.QueryAggregates(context.Background(), from, to)
	if err != nil {
		t.Fatalf("QueryAggregates: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Total != 20 {
		t.Errorf("total = %d, want 20", r.Total)
	}
	// P95 latency should be the 19th value (sorted): 100 + 19*10 = 290
	if r.P95LatencyMs != 290 {
		t.Errorf("p95_latency_ms = %d, want 290", r.P95LatencyMs)
	}
	// Error rate = 2/20 = 0.1
	if math.Abs(r.ErrorRate-0.1) > 0.001 {
		t.Errorf("error_rate = %f, want 0.1", r.ErrorRate)
	}
}
