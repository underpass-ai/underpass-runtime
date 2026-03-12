package main

import (
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"testing"
)

func TestEnvOrDefault(t *testing.T) {
	// With env set
	t.Setenv("TEST_SEED_KEY", "custom_value")
	if got := envOrDefault("TEST_SEED_KEY", "fallback"); got != "custom_value" {
		t.Errorf("envOrDefault = %q, want %q", got, "custom_value")
	}

	// Without env set (fallback)
	if got := envOrDefault("TEST_SEED_MISSING_KEY_XYZ", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault = %q, want %q", got, "fallback")
	}
}

func TestSafeBucketRegex(t *testing.T) {
	valid := []string{"telemetry-lake", "my.bucket.name", "abc123", "a-b"}
	for _, name := range valid {
		if !safeBucket.MatchString(name) {
			t.Errorf("safeBucket should match %q", name)
		}
	}

	invalid := []string{"", "a", "-invalid", "has space", "has/slash", "../escape"}
	for _, name := range invalid {
		if safeBucket.MatchString(name) {
			t.Errorf("safeBucket should not match %q", name)
		}
	}
}

func TestSafePathRegex(t *testing.T) {
	valid := []string{
		"s3://telemetry-lake",
		"/tmp/test-dir/output",
		"/home/user/data_export",
		"s3://my.bucket.name",
	}
	for _, p := range valid {
		if !safePath.MatchString(p) {
			t.Errorf("safePath should match %q", p)
		}
	}

	invalid := []string{
		"",
		"path with spaces",
		"s3://bucket'; DROP TABLE x--",
		"/tmp/dir'injection",
		"/tmp/$VAR",
		"/tmp/`cmd`",
	}
	for _, p := range invalid {
		if safePath.MatchString(p) {
			t.Errorf("safePath should NOT match %q", p)
		}
	}
}

func TestEscapeSQLLiteral(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"it's", "it''s"},
		{"a'b'c", "a''b''c"},
		{"no quotes", "no quotes"},
		{"", ""},
		{"single'", "single''"},
		{"''already''", "''''already''''"},
	}
	for _, tc := range tests {
		got := escapeSQLLiteral(tc.input)
		if got != tc.want {
			t.Errorf("escapeSQLLiteral(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExportParquetUnsafeDest(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	if _, genErr := generateData(db, 1, 5); genErr != nil {
		t.Fatalf("generateData: %v", genErr)
	}

	unsafePaths := []string{
		"s3://bucket'; DROP TABLE x--",
		"/tmp/dir'injection",
		"/tmp/$VAR",
		"/tmp/`cmd`",
	}
	for _, p := range unsafePaths {
		if err := exportParquet(db, p); err == nil {
			t.Errorf("exportParquet(%q) should fail for unsafe path", p)
		}
	}
}

func TestSeedLakeInvalidBucket(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	err := seedLake(seedConfig{Bucket: "../bad"}, logger)
	if err == nil {
		t.Fatal("expected error for invalid bucket")
	}
}

func TestGenerateData(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	count, err := generateData(db, 1, 10)
	if err != nil {
		t.Fatalf("generateData: %v", err)
	}
	// 1 hour * 10 per hour * 16 tool combos = 160
	if count != 160 {
		t.Errorf("count = %d, want 160", count)
	}
}

func TestExportParquetLocal(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	if _, genErr := generateData(db, 1, 5); genErr != nil {
		t.Fatalf("generateData: %v", genErr)
	}

	// Export to local temp dir — covers the success path.
	tmpDir := t.TempDir()
	if err := exportParquet(db, tmpDir); err != nil {
		t.Fatalf("exportParquet: %v", err)
	}
}

func TestExportParquetFails(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	if _, genErr := generateData(db, 1, 5); genErr != nil {
		t.Fatalf("generateData: %v", genErr)
	}

	err = exportParquet(db, "s3://nonexistent-bucket")
	if err == nil {
		t.Fatal("expected error exporting to non-existent S3")
	}
}

func TestRunS3Failure(t *testing.T) {
	// Reset flags for clean test
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"test", "--hours=1", "--invocations-per-hour=5"}
	t.Setenv("S3_ENDPOINT", "localhost:19999")
	t.Setenv("S3_ACCESS_KEY", "test")
	t.Setenv("S3_SECRET_KEY", "test")
	t.Setenv("LAKE_BUCKET", "test-bucket")

	err := run()
	if err == nil {
		t.Fatal("expected error from unreachable S3")
	}
}

func TestRunInvalidBucket(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"test", "--hours=1", "--invocations-per-hour=5"}
	t.Setenv("LAKE_BUCKET", "../bad")

	err := run()
	if err == nil {
		t.Fatal("expected error for invalid bucket")
	}
}

func TestCountPartitions(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	if _, genErr := generateData(db, 2, 10); genErr != nil {
		t.Fatalf("generateData: %v", genErr)
	}

	partitions, err := countPartitions(db)
	if err != nil {
		t.Fatalf("countPartitions: %v", err)
	}
	if partitions == 0 {
		t.Error("expected at least 1 partition")
	}
}

func TestCountPartitionsNoTable(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	_, err = countPartitions(db)
	if err == nil {
		t.Fatal("expected error for missing table")
	}
}

func TestSeedLakeLocalExportSuccess(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	err := seedLake(seedConfig{
		Hours:          1,
		PerHour:        2,
		Endpoint:       "localhost:19999",
		AccessKey:      "test",
		SecretKey:      "test",
		Region:         "us-east-1",
		UseSSL:         "false",
		Bucket:         "test-bucket",
		LocalExportDir: t.TempDir(),
	}, logger)
	if err != nil {
		t.Fatalf("seedLake with local export: %v", err)
	}
}

func TestSeedLakeS3ConfigError(t *testing.T) {
	// Use an endpoint that will cause S3 config to succeed but export to fail.
	// This exercises the full seedLake path: bucket validation → DuckDB open → S3 config → generate → export fail.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	err := seedLake(seedConfig{
		Hours:     1,
		PerHour:   2,
		Endpoint:  "localhost:19999",
		AccessKey: "test",
		SecretKey: "test",
		Region:    "us-east-1",
		UseSSL:    "false",
		Bucket:    "test-bucket",
	}, logger)
	if err == nil {
		t.Fatal("expected S3 export error")
	}
}

func TestSeedLakeQuotesInCredentials(t *testing.T) {
	// Credentials containing single quotes must not cause SQL injection.
	// DuckDB SET with escaped quotes should succeed without breaking SQL parsing.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	err := seedLake(seedConfig{
		Hours:          1,
		PerHour:        2,
		Endpoint:       "localhost:19999",
		AccessKey:      "user'key",
		SecretKey:      "pass'word",
		Region:         "us-east-1",
		UseSSL:         "false",
		Bucket:         "test-bucket",
		LocalExportDir: t.TempDir(),
	}, logger)
	// Should succeed — S3 config accepts any escaped string.
	// It will fail only at export if S3 is unreachable, but the SET commands must not break.
	if err != nil {
		t.Fatalf("seedLake with quoted credentials: %v", err)
	}
}

func TestSeedLakeS3Failure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	// Valid bucket but unreachable S3 — covers the full flow up to export failure.
	err := seedLake(seedConfig{
		Hours:     1,
		PerHour:   5,
		Endpoint:  "localhost:19999",
		AccessKey: "test",
		SecretKey: "test",
		Region:    "us-east-1",
		UseSSL:    "false",
		Bucket:    "test-bucket",
	}, logger)
	// Will fail at S3 export, covering all code before that.
	if err == nil {
		t.Fatal("expected error from unreachable S3")
	}
}
