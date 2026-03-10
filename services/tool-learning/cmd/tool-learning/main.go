package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/duckdb"
	natspub "github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/nats"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/s3"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/adapters/valkey"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/app"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	schedule := flag.String("schedule", "hourly", "Computation schedule: hourly, daily")
	maxLatency := flag.Int64("max-p95-latency-ms", 0, "Hard constraint: max p95 latency (0 = disabled)")
	maxErrorRate := flag.Float64("max-error-rate", 0, "Hard constraint: max error rate (0 = disabled)")
	maxCost := flag.Float64("max-p95-cost", 0, "Hard constraint: max p95 cost (0 = disabled)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))

	logger.Info("tool-learning service starting",
		"schedule", *schedule,
		"version", "0.1.0",
	)

	constraints := domain.PolicyConstraints{
		MaxP95LatencyMs: *maxLatency,
		MaxErrorRate:    *maxErrorRate,
		MaxP95Cost:      *maxCost,
	}

	lake, store, publisher, audit, cleanup, err := buildAdapters(logger, *schedule)
	if err != nil {
		return fmt.Errorf("build adapters: %w", err)
	}
	defer cleanup()

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:        lake,
		Store:       store,
		Publisher:   publisher,
		Audit:       audit,
		Constraints: constraints,
		Logger:      logger,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var result app.ComputeResult
	switch *schedule {
	case "hourly":
		result, err = uc.RunHourly(ctx)
	case "daily":
		result, err = uc.RunDaily(ctx)
	default:
		return fmt.Errorf("unknown schedule: %s", *schedule)
	}

	if err != nil {
		return fmt.Errorf("policy computation: %w", err)
	}

	logger.Info("policy computation succeeded",
		"aggregates_read", result.AggregatesRead,
		"policies_written", result.PoliciesWritten,
		"policies_filtered", result.PoliciesFiltered,
		"duration_ms", result.Duration.Milliseconds(),
	)
	return nil
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

// buildAdapters wires real adapters from environment variables.
// Returns a cleanup function for deferred shutdown.
func buildAdapters(logger *slog.Logger, schedule string) (
	app.TelemetryLakeReader,
	app.PolicyStore,
	app.PolicyEventPublisher,
	app.PolicyAuditStore,
	func(),
	error,
) {
	// TL-003: DuckDB lake reader
	s3Endpoint := envOrDefault("S3_ENDPOINT", "localhost:9000")
	s3AccessKey := envOrDefault("S3_ACCESS_KEY", "minioadmin")
	s3SecretKey := envOrDefault("S3_SECRET_KEY", "minioadmin")
	s3Region := envOrDefault("S3_REGION", "us-east-1")
	s3UseSSL := envOrDefault("S3_USE_SSL", "false") == "true"
	lakeBucket := envOrDefault("LAKE_BUCKET", "telemetry-lake")

	lake, err := duckdb.NewLakeReaderFromS3(s3Endpoint, s3AccessKey, s3SecretKey, lakeBucket, s3Region, s3UseSSL)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("duckdb lake reader: %w", err)
	}
	logger.Info("adapter ready", "adapter", "duckdb-lake-reader", "bucket", lakeBucket)

	// TL-004: Valkey policy store
	valkeyAddr := envOrDefault("VALKEY_ADDR", "localhost:6379")
	valkeyPassword := os.Getenv("VALKEY_PASSWORD")
	valkeyDB, _ := strconv.Atoi(envOrDefault("VALKEY_DB", "0"))
	valkeyPrefix := envOrDefault("VALKEY_KEY_PREFIX", "tool_policy")
	valkeyTTL, _ := time.ParseDuration(envOrDefault("VALKEY_TTL", "2h"))

	store, err := valkey.NewPolicyStoreFromAddress(context.Background(), valkeyAddr, valkeyPassword, valkeyDB, valkeyPrefix, valkeyTTL)
	if err != nil {
		lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("valkey policy store: %w", err)
	}
	logger.Info("adapter ready", "adapter", "valkey-policy-store", "addr", valkeyAddr)

	// TL-005: NATS event publisher
	natsURL := envOrDefault("NATS_URL", "nats://localhost:4222")

	pub, natsConn, err := natspub.NewPublisherFromURL(natsURL, schedule)
	if err != nil {
		lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("nats publisher: %w", err)
	}
	logger.Info("adapter ready", "adapter", "nats-publisher", "url", natsURL)

	// TL-006: S3 audit store
	auditBucket := envOrDefault("AUDIT_BUCKET", "policy-audit")

	audit, err := s3.NewAuditStoreFromConfig(s3Endpoint, s3AccessKey, s3SecretKey, auditBucket, s3UseSSL)
	if err != nil {
		lake.Close()
		natsConn.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("s3 audit store: %w", err)
	}
	logger.Info("adapter ready", "adapter", "s3-audit-store", "bucket", auditBucket)

	cleanup := func() {
		lake.Close()
		pub.Close()
		natsConn.Close()
	}

	return lake, store, pub, audit, cleanup, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
