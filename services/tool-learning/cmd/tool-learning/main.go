package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
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

	cfg, err := loadConfig(*schedule)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	lake, store, publisher, audit, cleanup, err := buildAdapters(cfg, logger)
	if err != nil {
		return fmt.Errorf("build adapters: %w", err)
	}
	defer cleanup()

	return execute(context.Background(), executeParams{
		Lake:        lake,
		Store:       store,
		Publisher:   publisher,
		Audit:       audit,
		Constraints: constraints,
		Schedule:    *schedule,
		Logger:      logger,
	})
}

// executeParams groups all parameters for execute(), avoiding long parameter lists.
type executeParams struct {
	Lake        app.TelemetryLakeReader
	Store       app.PolicyStore
	Publisher   app.PolicyEventPublisher
	Audit       app.PolicyAuditStore
	Constraints domain.PolicyConstraints
	Schedule    string
	Logger      *slog.Logger
}

// execute runs the policy computation pipeline. Extracted for testability.
func execute(parent context.Context, p executeParams) error {
	p.Logger.Info("tool-learning service starting", "schedule", p.Schedule, "version", "0.1.0")

	uc := app.NewComputePolicyUseCase(app.ComputePolicyConfig{
		Lake:        p.Lake,
		Store:       p.Store,
		Publisher:   p.Publisher,
		Audit:       p.Audit,
		Constraints: p.Constraints,
		Logger:      p.Logger,
	})

	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()

	var result app.ComputeResult
	var err error
	switch p.Schedule {
	case "hourly":
		result, err = uc.RunHourly(ctx)
	case "daily":
		result, err = uc.RunDaily(ctx)
	default:
		return fmt.Errorf("unknown schedule: %s", p.Schedule)
	}

	if err != nil {
		return fmt.Errorf("policy computation: %w", err)
	}

	p.Logger.Info("policy computation succeeded",
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

const logAdapterReady = "adapter ready"

// adapterConfig holds all configuration for building adapters.
type adapterConfig struct {
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3UseSSL    bool
	S3TLS       *tls.Config
	LakeBucket  string
	AuditBucket string
	ValkeyAddr  string
	ValkeyPass  string
	ValkeyDB    int
	ValkeyPfx   string
	ValkeyTTL   time.Duration
	ValkeyTLS   *tls.Config
	NATSURL     string
	NATSTLS     *tls.Config
	Schedule    string
}

// loadConfig reads adapter configuration from environment variables.
func loadConfig(schedule string) (adapterConfig, error) {
	valkeyDB, err := strconv.Atoi(envOrDefault("VALKEY_DB", "0"))
	if err != nil {
		return adapterConfig{}, fmt.Errorf("invalid VALKEY_DB: %w", err)
	}
	valkeyTTL, err := time.ParseDuration(envOrDefault("VALKEY_TTL", "2h"))
	if err != nil {
		return adapterConfig{}, fmt.Errorf("invalid VALKEY_TTL: %w", err)
	}
	valkeyTLS, err := buildClientTLS(
		os.Getenv("VALKEY_TLS_ENABLED") == "true",
		os.Getenv("VALKEY_TLS_CA_PATH"),
		os.Getenv("VALKEY_TLS_CERT_PATH"),
		os.Getenv("VALKEY_TLS_KEY_PATH"),
	)
	if err != nil {
		return adapterConfig{}, fmt.Errorf("valkey TLS: %w", err)
	}

	natsTLS, err := buildClientTLS(
		strings.TrimSpace(os.Getenv("NATS_TLS_MODE")) != "" && strings.TrimSpace(os.Getenv("NATS_TLS_MODE")) != "disabled",
		os.Getenv("NATS_TLS_CA_PATH"),
		os.Getenv("NATS_TLS_CERT_PATH"),
		os.Getenv("NATS_TLS_KEY_PATH"),
	)
	if err != nil {
		return adapterConfig{}, fmt.Errorf("nats TLS: %w", err)
	}

	s3TLS, err := buildClientTLS(
		envOrDefault("S3_USE_SSL", "false") == "true",
		os.Getenv("S3_CA_PATH"),
		"", "",
	)
	if err != nil {
		return adapterConfig{}, fmt.Errorf("s3 TLS: %w", err)
	}

	return adapterConfig{
		S3Endpoint:  envOrDefault("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey: envOrDefault("S3_ACCESS_KEY", ""),
		S3SecretKey: envOrDefault("S3_SECRET_KEY", ""),
		S3Region:    envOrDefault("S3_REGION", "us-east-1"),
		S3UseSSL:    envOrDefault("S3_USE_SSL", "false") == "true",
		S3TLS:       s3TLS,
		LakeBucket:  envOrDefault("LAKE_BUCKET", "telemetry-lake"),
		AuditBucket: envOrDefault("AUDIT_BUCKET", "policy-audit"),
		ValkeyAddr:  envOrDefault("VALKEY_ADDR", "localhost:6379"),
		ValkeyPass:  os.Getenv("VALKEY_PASSWORD"),
		ValkeyDB:    valkeyDB,
		ValkeyPfx:   envOrDefault("VALKEY_KEY_PREFIX", "tool_policy"),
		ValkeyTTL:   valkeyTTL,
		ValkeyTLS:   valkeyTLS,
		NATSURL:     envOrDefault("NATS_URL", "nats://localhost:4222"),
		NATSTLS:     natsTLS,
		Schedule:    schedule,
	}, nil
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
	logger.Info(logAdapterReady, "adapter", "duckdb-lake-reader", "bucket", cfg.LakeBucket)

	store, err := valkey.NewPolicyStoreFromAddress(context.Background(), cfg.ValkeyAddr, cfg.ValkeyPass, cfg.ValkeyDB, cfg.ValkeyPfx, cfg.ValkeyTTL, cfg.ValkeyTLS)
	if err != nil {
		_ = lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("valkey policy store: %w", err)
	}
	logger.Info(logAdapterReady, "adapter", "valkey-policy-store", "addr", cfg.ValkeyAddr)

	pub, natsConn, err := natspub.NewPublisherFromURL(cfg.NATSURL, cfg.Schedule, cfg.NATSTLS)
	if err != nil {
		_ = lake.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("nats publisher: %w", err)
	}
	logger.Info(logAdapterReady, "adapter", "nats-publisher", "url", cfg.NATSURL)

	audit, err := s3.NewAuditStoreFromConfig(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.AuditBucket, cfg.S3UseSSL, cfg.S3TLS)
	if err != nil {
		_ = lake.Close()
		natsConn.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("s3 audit store: %w", err)
	}
	logger.Info(logAdapterReady, "adapter", "s3-audit-store", "bucket", cfg.AuditBucket)

	cleanup := func() {
		_ = lake.Close()
		pub.Close()
		natsConn.Close()
	}

	return lake, store, pub, audit, cleanup, nil
}

// buildClientTLS builds a *tls.Config for outgoing connections.
// Returns nil when enabled is false (TLS disabled).
func buildClientTLS(enabled bool, caPath, certPath, keyPath string) (*tls.Config, error) {
	if !enabled {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		data, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read CA %s: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no valid certs in %s", caPath)
		}
		cfg.RootCAs = pool
	}
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
