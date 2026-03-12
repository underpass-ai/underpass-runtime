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

func TestExportToS3Fails(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("duckdb open: %v", err)
	}
	defer db.Close()

	// Generate data first
	if _, genErr := generateData(db, 1, 5); genErr != nil {
		t.Fatalf("generateData: %v", genErr)
	}

	// Export will fail — no S3 endpoint. This covers the error path.
	err = exportToS3(db, "nonexistent-bucket")
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
