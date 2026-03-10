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

	constraints := domain.PolicyConstraints{
		MaxP95LatencyMs: *maxLatency,
		MaxErrorRate:    *maxErrorRate,
		MaxP95Cost:      *maxCost,
	}

	cfg := loadConfig(*schedule)
	lake, store, publisher, audit, cleanup, err := buildAdapters(cfg, logger)
	if err != nil {
		return fmt.Errorf("build adapters: %w", err)
	}
	defer cleanup()

	return execute(context.Background(), lake, store, publisher, audit, constraints, *schedule, logger)
}

// execute runs the policy computation pipeline. Extracted for testability.
func execute(
	parent context.Context,
	lake app.TelemetryLakeReader,
	store app.PolicyStore,
	publisher app.PolicyEventPublisher,
	audit app.PolicyAuditStore,
	constraints domain.PolicyConstraints,
	schedule string,
	logger *slog.Logger,
) error {
	logger.Info("tool-learning service starting", "schedule", schedule, "version", "0.1.0")

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:        lake,
		Store:       store,
		Publisher:   publisher,
		Audit:       audit,
		Constraints: constraints,
		Logger:      logger,
	})

	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()

	var result app.ComputeResult
	var err error
	switch schedule {
	case "hourly":
		result, err = uc.RunHourly(ctx)
	case "daily":
		result, err = uc.RunDaily(ctx)
	default:
		return fmt.Errorf("unknown schedule: %s", schedule)
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

// adapterConfig holds all configuration for building adapters.
type adapterConfig struct {
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3UseSSL    bool
	LakeBucket  string
	AuditBucket string
	ValkeyAddr  string
	ValkeyPass  string
	ValkeyDB    int
	ValkeyPfx   string
	ValkeyTTL   time.Duration
	NATSURL     string
	Schedule    string
}

// loadConfig reads adapter configuration from environment variables.
func loadConfig(schedule string) adapterConfig {
	valkeyDB, _ := strconv.Atoi(envOrDefault("VALKEY_DB", "0"))
	valkeyTTL, _ := time.ParseDuration(envOrDefault("VALKEY_TTL", "2h"))
	return adapterConfig{
		S3Endpoint:  envOrDefault("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey: envOrDefault("S3_ACCESS_KEY", ""),
		S3SecretKey: envOrDefault("S3_SECRET_KEY", ""),
		S3Region:    envOrDefault("S3_REGION", "us-east-1"),
		S3UseSSL:    envOrDefault("S3_USE_SSL", "false") == "true",
		LakeBucket:  envOrDefault("LAKE_BUCKET", "telemetry-lake"),
		AuditBucket: envOrDefault("AUDIT_BUCKET", "policy-audit"),
		ValkeyAddr:  envOrDefault("VALKEY_ADDR", "localhost:6379"),
		ValkeyPass:  os.Getenv("VALKEY_PASSWORD"),
		ValkeyDB:    valkeyDB,
		ValkeyPfx:   envOrDefault("VALKEY_KEY_PREFIX", "tool_policy"),
		ValkeyTTL:   valkeyTTL,
		NATSURL:     envOrDefault("NATS_URL", "nats://localhost:4222"),
		Schedule:    schedule,
	}
}

// buildAdapters wires real adapters from configuration.
// Returns a cleanup function for deferred shutdown.
func buildAdapters(cfg adapterConfig, logger *slog.Logger) (
	app.TelemetryLakeReader,
	app.PolicyStore,
	app.PolicyEventPublisher,
	app.PolicyAuditStore,
	func(),
	error,
) {
	lake, err := duckdb.NewLakeReaderFromS3(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.LakeBucket, cfg.S3Region, cfg.S3UseSSL)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("duckdb lake reader: %w", err)
	}
	logger.Info("adapter ready", "adapter", "duckdb-lake-reader", "bucket", cfg.LakeBucket)

	store, err := valkey.NewPolicyStoreFromAddress(context.Background(), cfg.ValkeyAddr, cfg.ValkeyPass, cfg.ValkeyDB, cfg.ValkeyPfx, cfg.ValkeyTTL)
	if err != nil {
		_ = lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("valkey policy store: %w", err)
	}
	logger.Info("adapter ready", "adapter", "valkey-policy-store", "addr", cfg.ValkeyAddr)

	pub, natsConn, err := natspub.NewPublisherFromURL(cfg.NATSURL, cfg.Schedule)
	if err != nil {
		_ = lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("nats publisher: %w", err)
	}
	logger.Info("adapter ready", "adapter", "nats-publisher", "url", cfg.NATSURL)

	audit, err := s3.NewAuditStoreFromConfig(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.AuditBucket, cfg.S3UseSSL)
	if err != nil {
		_ = lake.Close()
		natsConn.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("s3 audit store: %w", err)
	}
	logger.Info("adapter ready", "adapter", "s3-audit-store", "bucket", cfg.AuditBucket)

	cleanup := func() {
		_ = lake.Close()
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
