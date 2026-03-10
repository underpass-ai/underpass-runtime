package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/marcboeker/go-duckdb"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// LakeReader implements app.TelemetryLakeReader using DuckDB
// to query Parquet files from an S3-compatible object store (MinIO).
type LakeReader struct {
	db     *sql.DB
	source string // table name (test) or read_parquet(...) expression (production)
}

// NewLakeReader creates a reader with a pre-configured DuckDB database.
func NewLakeReader(db *sql.DB, source string) *LakeReader {
	return &LakeReader{db: db, source: source}
}

// NewLakeReaderFromS3 creates a reader configured for MinIO/S3.
func NewLakeReaderFromS3(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*LakeReader, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("duckdb open: %w", err)
	}

	sslFlag := "false"
	if useSSL {
		sslFlag = "true"
	}

	configs := [][2]string{
		{"INSTALL httpfs", "install httpfs"},
		{"LOAD httpfs", "load httpfs"},
		{"SET s3_endpoint='" + endpoint + "'", "set s3_endpoint"},
		{"SET s3_access_key_id='" + accessKey + "'", "set s3_access_key_id"},
		{"SET s3_secret_access_key='" + secretKey + "'", "set s3_secret_access_key"},
		{"SET s3_region='" + region + "'", "set s3_region"},
		{"SET s3_use_ssl=" + sslFlag, "set s3_use_ssl"},
		{"SET s3_url_style='path'", "set s3_url_style"},
	}

	for _, cfg := range configs {
		if _, err := db.ExecContext(context.Background(), cfg[0]); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("duckdb %s: %w", cfg[1], err)
		}
	}

	source := fmt.Sprintf(
		"read_parquet('s3://%s/dt=*/hour=*/*.parquet', hive_partitioning=true)",
		bucket,
	)
	return &LakeReader{db: db, source: source}, nil
}

const aggregateQuery = `
SELECT
    context_signature,
    tool_id,
    CAST(COUNT(*) AS BIGINT) AS total,
    CAST(COUNT(*) FILTER (WHERE outcome = 'success') AS BIGINT) AS successes,
    CAST(COUNT(*) FILTER (WHERE outcome = 'failure') AS BIGINT) AS failures,
    CAST(COALESCE(PERCENTILE_DISC(0.95) WITHIN GROUP (ORDER BY latency_ms), 0) AS BIGINT) AS p95_latency_ms,
    COALESCE(PERCENTILE_DISC(0.95) WITHIN GROUP (ORDER BY cost_units), 0.0) AS p95_cost,
    AVG(CASE WHEN outcome = 'failure' THEN 1.0 ELSE 0.0 END) AS error_rate
FROM %s
WHERE ts >= ? AND ts < ?
GROUP BY context_signature, tool_id
`

// QueryAggregates scans invocations in [from, to) and returns per-(context, tool) aggregates.
func (r *LakeReader) QueryAggregates(ctx context.Context, from, to time.Time) ([]domain.AggregateStats, error) {
	query := fmt.Sprintf(aggregateQuery, r.source)

	rows, err := r.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("duckdb query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []domain.AggregateStats
	for rows.Next() {
		var s domain.AggregateStats
		if err := rows.Scan(
			&s.ContextSignature,
			&s.ToolID,
			&s.Total,
			&s.Successes,
			&s.Failures,
			&s.P95LatencyMs,
			&s.P95Cost,
			&s.ErrorRate,
		); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// Close releases the DuckDB database connection.
func (r *LakeReader) Close() error {
	return r.db.Close()
}
