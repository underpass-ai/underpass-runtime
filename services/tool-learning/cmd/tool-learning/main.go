package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/app"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func main() {
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

	lake, store, publisher, audit, cleanup, err := buildAdapters(logger)
	if err != nil {
		logger.Error("failed to build adapters", "error", err)
		os.Exit(1)
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
		logger.Error("unknown schedule", "schedule", *schedule)
		os.Exit(1)
	}

	if err != nil {
		logger.Error("policy computation failed", "error", err)
		os.Exit(1)
	}

	logger.Info("policy computation succeeded",
		"aggregates_read", result.AggregatesRead,
		"policies_written", result.PoliciesWritten,
		"policies_filtered", result.PoliciesFiltered,
		"duration_ms", result.Duration.Milliseconds(),
	)
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
func buildAdapters(logger *slog.Logger) (
	app.TelemetryLakeReader,
	app.PolicyStore,
	app.PolicyEventPublisher,
	app.PolicyAuditStore,
	func(),
	error,
) {
	// TODO(TL-003): DuckDB + MinIO lake reader
	// TODO(TL-004): Valkey policy store
	// TODO(TL-005): NATS event publisher
	// TODO(TL-006): S3 audit store

	logger.Warn("adapters not yet implemented, using stubs")

	// Temporary stubs until adapters are implemented.
	// These will be replaced in TL-003 through TL-006.
	stub := &stubAdapters{}
	return stub, stub, stub, stub, func() {}, nil
}

// stubAdapters implements all ports as no-ops for initial wiring.
type stubAdapters struct{}

func (s *stubAdapters) QueryAggregates(_ context.Context, _, _ time.Time) ([]domain.AggregateStats, error) {
	return nil, nil
}

func (s *stubAdapters) WritePolicy(_ context.Context, _ domain.ToolPolicy) error { return nil }

func (s *stubAdapters) WritePolicies(_ context.Context, _ []domain.ToolPolicy) error { return nil }

func (s *stubAdapters) ReadPolicy(_ context.Context, _, _ string) (domain.ToolPolicy, bool, error) {
	return domain.ToolPolicy{}, false, nil
}

func (s *stubAdapters) PublishPolicyUpdated(_ context.Context, _ []domain.ToolPolicy) error {
	return nil
}

func (s *stubAdapters) WriteSnapshot(_ context.Context, _ time.Time, _ []domain.ToolPolicy) error {
	return nil
}
