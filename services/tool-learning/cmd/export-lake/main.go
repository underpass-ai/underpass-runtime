// export-lake reads telemetry records from Valkey and writes them as
// Hive-partitioned Parquet files to the S3 telemetry lake. Designed to
// run as a CronJob before the tool-learning policy computation.
//
// Usage:
//
//	export-lake
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/redis/go-redis/v9"
)

var safeBucket = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-]{1,61}[a-zA-Z0-9]$`)
var safePath = regexp.MustCompile(`^[a-zA-Z0-9/:._\-]+$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))

	ctx := context.Background()

	// Connect to Valkey.
	valkeyClient, err := buildValkeyClient(ctx)
	if err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	logger.Info("connected to Valkey")

	// Read telemetry records from all tool keys.
	prefix := envOrDefault("VALKEY_TELEMETRY_PREFIX", "workspace:telemetry:")
	records, err := readAllTelemetry(ctx, valkeyClient, prefix, logger)
	if err != nil {
		return fmt.Errorf("read telemetry: %w", err)
	}
	if len(records) == 0 {
		logger.Info("no telemetry records to export")
		return nil
	}
	logger.Info("read telemetry records", "count", len(records))

	// Open DuckDB and configure S3.
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return fmt.Errorf("duckdb: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := configureS3(db); err != nil {
		return err
	}

	// Load records into DuckDB table.
	if err := loadRecords(db, records); err != nil {
		return fmt.Errorf("load records: %w", err)
	}
	logger.Info("loaded records into DuckDB", "count", len(records))

	// Export to Parquet.
	bucket := envOrDefault("LAKE_BUCKET", "telemetry-lake")
	if !safeBucket.MatchString(bucket) {
		return fmt.Errorf("invalid bucket: %q", bucket)
	}
	dest := "s3://" + bucket
	if err := exportParquet(db, dest); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	logger.Info("export complete", "records", len(records), "bucket", bucket)
	return nil
}

// telemetryRecord mirrors the runtime TelemetryRecord for JSON unmarshaling.
type telemetryRecord struct {
	InvocationID  string    `json:"invocation_id"`
	SessionID     string    `json:"session_id"`
	ToolName      string    `json:"tool_name"`
	ToolFamily    string    `json:"tool_family"`
	RuntimeKind   string    `json:"runtime_kind"`
	RepoLanguage  string    `json:"repo_language"`
	ProjectType   string    `json:"project_type"`
	TenantID      string    `json:"tenant_id"`
	Approved      bool      `json:"approved"`
	Status        string    `json:"status"`
	ErrorCode     string    `json:"error_code"`
	DurationMs    int64     `json:"duration_ms"`
	OutputBytes   int64     `json:"output_bytes"`
	ArtifactCount int       `json:"artifact_count"`
	ContextSig    string    `json:"context_sig"`
	Timestamp     time.Time `json:"timestamp"`
}

func (r telemetryRecord) outcome() string {
	if r.Status == "denied" {
		return "failure"
	}
	if r.Status == "succeeded" {
		return "success"
	}
	return "failure"
}

// estimateCost derives cost_units from tool family and duration.
func (r telemetryRecord) estimateCost() float64 {
	base := map[string]float64{
		"fs": 0.05, "git": 0.08, "repo": 0.50,
		"docker": 0.40, "k8s": 0.60, "conn": 0.10,
		"security": 0.20, "artifact": 0.05,
	}
	b := base[r.ToolFamily]
	if b == 0 {
		b = 0.10
	}
	return b + float64(r.DurationMs)/10000.0
}

func readAllTelemetry(ctx context.Context, client *redis.Client, prefix string, logger *slog.Logger) ([]telemetryRecord, error) {
	var cursor uint64
	var allRecords []telemetryRecord

	for {
		keys, next, err := client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		for _, key := range keys {
			items, lErr := client.LRange(ctx, key, -10000, -1).Result()
			if lErr != nil {
				logger.Warn("lrange failed", "key", key, "error", lErr)
				continue
			}
			for _, item := range items {
				var rec telemetryRecord
				if jErr := json.Unmarshal([]byte(item), &rec); jErr != nil {
					continue
				}
				allRecords = append(allRecords, rec)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return allRecords, nil
}

func loadRecords(db *sql.DB, records []telemetryRecord) error {
	createSQL := `
CREATE TABLE invocations (
    invocation_id VARCHAR,
    ts TIMESTAMP,
    tool_id VARCHAR,
    agent_id_hash VARCHAR,
    task_id VARCHAR,
    context_signature VARCHAR,
    outcome VARCHAR,
    error_type VARCHAR,
    latency_ms BIGINT,
    cost_units DOUBLE,
    tool_version VARCHAR,
    dt VARCHAR,
    "hour" VARCHAR
)`
	if _, err := db.ExecContext(context.Background(), createSQL); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	stmt, err := db.PrepareContext(context.Background(),
		`INSERT INTO invocations VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range records {
		ts := r.Timestamp.UTC()
		dt := ts.Format("2006-01-02")
		hour := fmt.Sprintf("%d", ts.Hour())
		agentHash := fmt.Sprintf("agent-%s", r.TenantID)
		taskID := fmt.Sprintf("task-%s", r.SessionID)

		_, err := stmt.ExecContext(context.Background(),
			r.InvocationID, ts, r.ToolName, agentHash, taskID,
			r.ContextSig, r.outcome(), r.ErrorCode,
			r.DurationMs, r.estimateCost(), "v1.0.0",
			dt, hour,
		)
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}
	return nil
}

func exportParquet(db *sql.DB, dest string) error {
	if !safePath.MatchString(dest) {
		return fmt.Errorf("unsafe destination: %q", dest)
	}
	exportSQL := `
COPY (
    SELECT invocation_id, ts, tool_id, agent_id_hash, task_id,
           context_signature, outcome, error_type,
           latency_ms, cost_units, tool_version,
           dt, "hour"
    FROM invocations
)
TO '` + dest + `'
(FORMAT PARQUET, PARTITION_BY (dt, "hour"), OVERWRITE_OR_IGNORE, FILENAME_PATTERN 'invocations-{uuid}')
`
	_, err := db.ExecContext(context.Background(), exportSQL)
	return err
}

func configureS3(db *sql.DB) error {
	escapeSQLLiteral := func(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	}
	cmds := []string{
		"INSTALL httpfs",
		"LOAD httpfs",
		fmt.Sprintf("SET s3_endpoint='%s'", escapeSQLLiteral(envOrDefault("S3_ENDPOINT", "localhost:9000"))),
		fmt.Sprintf("SET s3_access_key_id='%s'", escapeSQLLiteral(envOrDefault("S3_ACCESS_KEY", ""))),
		fmt.Sprintf("SET s3_secret_access_key='%s'", escapeSQLLiteral(envOrDefault("S3_SECRET_KEY", ""))),
		fmt.Sprintf("SET s3_region='%s'", escapeSQLLiteral(envOrDefault("S3_REGION", "us-east-1"))),
		fmt.Sprintf("SET s3_use_ssl=%s", escapeSQLLiteral(envOrDefault("S3_USE_SSL", "false"))),
		"SET s3_url_style='path'",
	}
	for _, cmd := range cmds {
		if _, err := db.ExecContext(context.Background(), cmd); err != nil {
			return fmt.Errorf("duckdb s3 config: %w", err)
		}
	}
	return nil
}

func buildValkeyClient(ctx context.Context) (*redis.Client, error) {
	addr := envOrDefault("VALKEY_ADDR", "localhost:6379")
	opts := &redis.Options{
		Addr:     addr,
		Password: os.Getenv("VALKEY_PASSWORD"),
		DB:       0,
	}
	if os.Getenv("VALKEY_TLS_CA_PATH") != "" {
		cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: os.Getenv("VALKEY_TLS_SERVER_NAME")}
		caData, err := os.ReadFile(os.Getenv("VALKEY_TLS_CA_PATH"))
		if err != nil {
			return nil, fmt.Errorf("read CA: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caData)
		cfg.RootCAs = pool
		if certPath := os.Getenv("VALKEY_TLS_CERT_PATH"); certPath != "" {
			cert, err := tls.LoadX509KeyPair(certPath, os.Getenv("VALKEY_TLS_KEY_PATH"))
			if err != nil {
				return nil, fmt.Errorf("load cert: %w", err)
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
		opts.TLSConfig = cfg
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return client, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(raw string) slog.Level {
	switch raw {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
