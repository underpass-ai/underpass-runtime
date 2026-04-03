package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

func TestLoadAndExportRecords(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	records := []telemetryRecord{
		{
			InvocationID: "inv-1",
			ToolName:     "fs.write",
			ToolFamily:   "fs",
			ContextSig:   "gen:go:std",
			Status:       "succeeded",
			DurationMs:   120,
			OutputBytes:  1024,
			TenantID:     "t1",
			SessionID:    "s1",
			Timestamp:    time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		},
		{
			InvocationID: "inv-2",
			ToolName:     "git.commit",
			ToolFamily:   "git",
			ContextSig:   "gen:go:std",
			Status:       "failed",
			ErrorCode:    "timeout",
			DurationMs:   5000,
			TenantID:     "t1",
			SessionID:    "s1",
			Timestamp:    time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC),
		},
		{
			InvocationID: "inv-3",
			ToolName:     "fs.read",
			ToolFamily:   "fs",
			ContextSig:   "gen:go:std",
			Status:       "denied",
			DurationMs:   0,
			TenantID:     "t1",
			SessionID:    "s2",
			Timestamp:    time.Date(2026, 4, 3, 10, 30, 0, 0, time.UTC),
		},
	}

	if err := loadRecords(db, records); err != nil {
		t.Fatalf("load records: %v", err)
	}

	// Verify row count.
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM invocations").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}

	// Verify outcome mapping.
	var successCount, failureCount int64
	db.QueryRow("SELECT COUNT(*) FROM invocations WHERE outcome = 'success'").Scan(&successCount)
	db.QueryRow("SELECT COUNT(*) FROM invocations WHERE outcome = 'failure'").Scan(&failureCount)
	if successCount != 1 {
		t.Fatalf("expected 1 success, got %d", successCount)
	}
	if failureCount != 2 {
		t.Fatalf("expected 2 failures (1 failed + 1 denied), got %d", failureCount)
	}

	// Verify partitions.
	var partitions int64
	db.QueryRow(`SELECT COUNT(DISTINCT dt || '/' || "hour") FROM invocations`).Scan(&partitions)
	if partitions != 2 {
		t.Fatalf("expected 2 partitions (hour 10, 11), got %d", partitions)
	}

	// Export to local dir.
	exportDir := filepath.Join(t.TempDir(), "lake")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := exportParquet(db, exportDir); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Verify Parquet files exist.
	entries, _ := os.ReadDir(exportDir)
	if len(entries) == 0 {
		t.Fatal("expected Parquet partition directories")
	}
}

func TestOutcomeMapping(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"succeeded", "success"},
		{"failed", "failure"},
		{"denied", "failure"},
		{"unknown", "failure"},
	}
	for _, tt := range tests {
		r := telemetryRecord{Status: tt.status}
		if got := r.outcome(); got != tt.want {
			t.Errorf("outcome(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestEstimateCost(t *testing.T) {
	r := telemetryRecord{ToolFamily: "fs", DurationMs: 100}
	cost := r.estimateCost()
	if cost < 0.05 || cost > 0.10 {
		t.Fatalf("expected cost ~0.06, got %f", cost)
	}

	r2 := telemetryRecord{ToolFamily: "unknown", DurationMs: 1000}
	cost2 := r2.estimateCost()
	if cost2 < 0.10 {
		t.Fatalf("expected cost >= 0.10 for unknown family, got %f", cost2)
	}
}
