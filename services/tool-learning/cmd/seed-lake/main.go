// seed-lake generates synthetic telemetry data and writes Hive-partitioned
// Parquet files to an S3-compatible object store (MinIO).
//
// Usage:
//
//	seed-lake --hours=24 --invocations-per-hour=200
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"

	_ "github.com/marcboeker/go-duckdb" // register DuckDB SQL driver
)

// safeBucket validates that a bucket name contains only alphanumeric, hyphens and dots.
var safeBucket = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-]{1,61}[a-zA-Z0-9]$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	hours := flag.Int("hours", 24, "Number of past hours to generate data for")
	perHour := flag.Int("invocations-per-hour", 200, "Invocations per hour")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	return seedLake(seedConfig{
		Hours:     *hours,
		PerHour:   *perHour,
		Endpoint:  envOrDefault("S3_ENDPOINT", "localhost:9000"),
		AccessKey: envOrDefault("S3_ACCESS_KEY", "minioadmin"),
		SecretKey: envOrDefault("S3_SECRET_KEY", "minioadmin"),
		Region:    envOrDefault("S3_REGION", "us-east-1"),
		UseSSL:    envOrDefault("S3_USE_SSL", "false"),
		Bucket:    envOrDefault("LAKE_BUCKET", "telemetry-lake"),
	}, logger)
}

// seedConfig holds all parameters for data generation.
type seedConfig struct {
	Hours     int
	PerHour   int
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    string
	Bucket    string
}

// seedLake generates synthetic telemetry and exports to S3.
func seedLake(cfg seedConfig, logger *slog.Logger) error {
	if !safeBucket.MatchString(cfg.Bucket) {
		return fmt.Errorf("invalid bucket name: %q", cfg.Bucket)
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return fmt.Errorf("duckdb open: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Configure S3
	s3Configs := [][2]string{
		{"INSTALL httpfs", "install httpfs"},
		{"LOAD httpfs", "load httpfs"},
		{fmt.Sprintf("SET s3_endpoint='%s'", cfg.Endpoint), "set s3_endpoint"},
		{fmt.Sprintf("SET s3_access_key_id='%s'", cfg.AccessKey), "set s3_access_key_id"},
		{fmt.Sprintf("SET s3_secret_access_key='%s'", cfg.SecretKey), "set s3_secret_access_key"},
		{fmt.Sprintf("SET s3_region='%s'", cfg.Region), "set s3_region"},
		{fmt.Sprintf("SET s3_use_ssl=%s", cfg.UseSSL), "set s3_use_ssl"},
		{"SET s3_url_style='path'", "set s3_url_style"},
	}

	for _, c := range s3Configs {
		if _, execErr := db.ExecContext(context.Background(), c[0]); execErr != nil {
			return fmt.Errorf("duckdb %s: %w", c[1], execErr)
		}
	}

	logger.Info("S3 configured", "endpoint", cfg.Endpoint, "bucket", cfg.Bucket)

	count, err := generateData(db, cfg.Hours, cfg.PerHour)
	if err != nil {
		return err
	}
	logger.Info("generated synthetic invocations", "count", count, "hours", cfg.Hours)

	if exportErr := exportToS3(db, cfg.Bucket); exportErr != nil {
		return exportErr
	}

	partitions, err := countPartitions(db)
	if err != nil {
		return err
	}

	logger.Info("seed complete",
		"invocations", count,
		"partitions", partitions,
		"bucket", cfg.Bucket,
	)

	return nil
}

// countPartitions returns the number of distinct date/hour partitions in the invocations table.
func countPartitions(db *sql.DB) (int64, error) {
	var partitions int64
	verifySQL := `SELECT COUNT(DISTINCT dt || '/' || "hour") FROM invocations`
	if err := db.QueryRowContext(context.Background(), verifySQL).Scan(&partitions); err != nil {
		return 0, fmt.Errorf("verify: %w", err)
	}
	return partitions, nil
}

// generateData creates the in-memory invocations table using DuckDB.
func generateData(db *sql.DB, hours, perHour int) (int64, error) {
	minuteSpan := strconv.Itoa(hours * 60)
	totalRows := strconv.Itoa(hours * perHour)
	genSQL := `
CREATE TABLE invocations AS
SELECT
    'inv-' || gen_random_uuid()::VARCHAR AS invocation_id,
    ts,
    strftime(ts, '%Y-%m-%d') AS dt,
    CAST(hour(ts) AS VARCHAR) AS "hour",
    tool_id,
    'agent-' || (abs(hash(gen_random_uuid())) % 5 + 1)::VARCHAR AS agent_id_hash,
    'task-' || (abs(hash(gen_random_uuid())) % 20 + 1)::VARCHAR AS task_id,
    context_signature,
    CASE
        WHEN random() < error_rate THEN 'failure'
        ELSE 'success'
    END AS outcome,
    CASE
        WHEN random() < error_rate THEN
            (ARRAY['timeout', 'permission_denied', 'not_found', 'internal'])[
                CAST(abs(hash(gen_random_uuid())) % 4 + 1 AS BIGINT)]
        ELSE ''
    END AS error_type,
    CAST(base_latency + abs(hash(gen_random_uuid())) % variance AS BIGINT) AS latency_ms,
    ROUND(base_cost + random() * cost_variance, 4) AS cost_units,
    'v1.0.0' AS tool_version
FROM (
    SELECT
        now()::TIMESTAMP - INTERVAL (abs(hash(gen_random_uuid())) % ` + minuteSpan + `) MINUTE AS ts,
        tool_id,
        context_signature,
        error_rate,
        base_latency,
        variance,
        base_cost,
        cost_variance
    FROM generate_series(1, ` + totalRows + `) AS _(i)
    CROSS JOIN (VALUES
        ('fs.write',    'gen:go:std',       0.05,  80,  120, 0.08, 0.04),
        ('fs.read',     'gen:go:std',       0.02,  30,   40, 0.03, 0.02),
        ('fs.search',   'gen:go:std',       0.03, 120,  200, 0.12, 0.06),
        ('git.status',  'gen:go:std',       0.01,  50,   60, 0.05, 0.02),
        ('git.diff',    'gen:go:std',       0.02,  70,  100, 0.06, 0.03),
        ('git.commit',  'gen:go:std',       0.04, 150,  300, 0.15, 0.08),
        ('repo.build',  'gen:go:std',       0.08, 500, 2000, 0.50, 0.30),
        ('repo.test',   'gen:go:std',       0.10, 800, 3000, 0.80, 0.40),
        ('fs.write',    'gen:python:std',   0.06,  90,  130, 0.09, 0.05),
        ('fs.read',     'gen:python:std',   0.02,  35,   45, 0.04, 0.02),
        ('repo.build',  'gen:python:std',   0.07, 400, 1500, 0.40, 0.25),
        ('repo.test',   'gen:python:std',   0.12, 600, 2500, 0.60, 0.35),
        ('fs.write',    'review:go:strict', 0.03,  70,  100, 0.07, 0.03),
        ('fs.read',     'review:go:strict', 0.01,  25,   35, 0.03, 0.01),
        ('git.commit',  'review:go:strict', 0.02, 130,  250, 0.13, 0.06),
        ('security.scan','gen:go:std',      0.05, 200,  500, 0.20, 0.10)
    ) AS tools(tool_id, context_signature, error_rate, base_latency, variance, base_cost, cost_variance)
)
`

	if _, err := db.ExecContext(context.Background(), genSQL); err != nil {
		return 0, fmt.Errorf("generate data: %w", err)
	}

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM invocations").Scan(&count); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return count, nil
}

// exportToS3 writes the invocations table as Hive-partitioned Parquet to S3.
func exportToS3(db *sql.DB, bucket string) error {
	exportSQL := `
COPY (
    SELECT
        invocation_id, ts, tool_id, agent_id_hash, task_id,
        context_signature, outcome, error_type,
        latency_ms, cost_units, tool_version,
        dt, "hour"
    FROM invocations
)
TO 's3://` + bucket + `'
(FORMAT PARQUET, PARTITION_BY (dt, "hour"), OVERWRITE_OR_IGNORE, FILENAME_PATTERN 'invocations-{uuid}')
`
	if _, err := db.ExecContext(context.Background(), exportSQL); err != nil {
		return fmt.Errorf("export to S3: %w", err)
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
