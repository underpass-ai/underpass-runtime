package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/nats-io/nats.go"

	pb "github.com/underpass-ai/underpass-runtime/gen/underpass/runtime/v1"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/audit"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/eventbus"
	invocationstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/invocationstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/policy"
	sessionstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/sessionstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	telemetryadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/telemetry"
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/bootstrap"
	"github.com/underpass-ai/underpass-runtime/internal/grpcapi"
	"github.com/underpass-ai/underpass-runtime/internal/tlsutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

const (
	workspaceBackendLocal  = "local"
	workspaceBackendDocker = "docker"
	defaultNamespace       = "underpass-runtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("LOG_LEVEL"))}))

	telemetryShutdown, err := setupTelemetry(context.Background(), logger)
	if err != nil {
		logger.Error("failed to initialize telemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := telemetryShutdown(shutdownCtx); shutdownErr != nil {
			logger.Warn("failed to shutdown telemetry", "error", shutdownErr)
		}
	}()

	port := envOrDefault("PORT", "50053")
	workspaceRoot := envOrDefault("WORKSPACE_ROOT", "/tmp/underpass-workspaces")
	artifactRoot := envOrDefault("ARTIFACT_ROOT", "/tmp/underpass-artifacts")
	workspaceBackend := strings.ToLower(strings.TrimSpace(envOrDefault("WORKSPACE_BACKEND", workspaceBackendLocal)))
	workspaceNamespace := envOrDefault("WORKSPACE_K8S_NAMESPACE", defaultNamespace)

	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		logger.Error("failed to create artifact root", "error", err)
		os.Exit(1)
	}

	k8s, err := initKubernetesRuntime(workspaceBackend)
	if err != nil {
		logger.Error("failed to initialize kubernetes client", "error", err)
		os.Exit(1)
	}

	// --- TLS setup ---
	valkeyTLS, err := buildValkeyTLS(logger)
	if err != nil {
		logger.Error("failed to build Valkey TLS config", "error", err)
		os.Exit(1)
	}
	natsTLS, err := buildNATSTLS(logger)
	if err != nil {
		logger.Error("failed to build NATS TLS config", "error", err)
		os.Exit(1)
	}
	serverTLS, err := buildServerTLS(logger)
	if err != nil {
		logger.Error("failed to build server TLS config", "error", err)
		os.Exit(1)
	}

	sessionStore, err := buildSessionStore(logger, valkeyTLS)
	if err != nil {
		logger.Error("failed to initialize session store", "error", err)
		os.Exit(1)
	}

	workspaceManager, err := buildWorkspaceManager(workspaceBackend, workspaceRoot, k8s, sessionStore)
	if err != nil {
		logger.Error("failed to initialize workspace manager", "error", err)
		os.Exit(1)
	}
	podJanitorCancel := startPodJanitorIfEnabled(workspaceBackend, workspaceNamespace, k8s, sessionStore, logger)
	catalog := tooladapter.NewCatalog(tooladapter.DefaultCapabilities())
	commandRunner, err := buildCommandRunner(workspaceBackend, k8s)
	if err != nil {
		logger.Error("failed to initialize command runner", "error", err)
		os.Exit(1)
	}
	registry := buildToolRegistry(k8s, workspaceNamespace)
	engine := registry.BuildEngine(bootstrap.Config{
		CommandRunner: commandRunner,
		K8sClient:     k8sClientOrNil(k8s),
		K8sNamespace:  workspaceNamespace,
		DockerClient:  dockerClientOrNil(workspaceBackend),
	})
	artifactStore, err := buildArtifactStore(context.Background(), artifactRoot, logger)
	if err != nil {
		logger.Error("failed to initialize artifact store", "error", err)
		os.Exit(1)
	}
	policyEngine := policy.NewStaticPolicy()
	auditLogger := audit.NewLoggerAudit(logger)
	invocationStore, err := buildInvocationStore(logger, valkeyTLS)
	if err != nil {
		logger.Error("failed to initialize invocation store", "error", err)
		os.Exit(1)
	}

	service := app.NewService(workspaceManager, catalog, policyEngine, engine, artifactStore, auditLogger, invocationStore)
	eventPub, natsConn, relayStop := buildEventBus(context.Background(), logger, natsTLS, valkeyTLS)
	service.SetEventPublisher(eventPub)
	telRecorder, telQuerier, telStop := buildTelemetry(context.Background(), logger, valkeyTLS)
	service.SetTelemetry(telRecorder, telQuerier)
	service.SetKPIMetrics(app.NewKPIMetrics())
	authConfig, err := grpcapi.AuthConfigFromEnv()
	if err != nil {
		logger.Error("failed to initialize auth configuration", "error", err)
		os.Exit(1)
	}
	grpcServer := grpcapi.NewServer(service, authConfig, logger)

	grpcOpts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcapi.UnaryAuthInterceptor(authConfig)),
	}
	if serverTLS != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(serverTLS)))
	}
	srv := grpc.NewServer(grpcOpts...)
	pb.RegisterSessionServiceServer(srv, grpcServer)
	pb.RegisterCapabilityCatalogServiceServer(srv, grpcServer)
	pb.RegisterInvocationServiceServer(srv, grpcServer)
	pb.RegisterHealthServiceServer(srv, grpcServer)
	reflection.Register(srv)

	// Prometheus metrics remain on HTTP (separate port).
	metricsPort := envOrDefault("METRICS_PORT", "9090")
	metricsServer := &http.Server{
		Addr:              ":" + metricsPort,
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = w.Write([]byte(service.PrometheusMetrics()))
		}),
	}
	go func() {
		logger.Info("metrics server listening", "port", metricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	startGRPCServer(srv, port, workspaceRoot, serverTLS, logger)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	if podJanitorCancel != nil {
		podJanitorCancel()
	}
	if relayStop != nil {
		relayStop()
	}
	if telStop != nil {
		telStop()
	}
	if natsConn != nil {
		natsConn.Close()
	}

	srv.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = metricsServer.Shutdown(ctx)
	logger.Info("workspace service stopped")
}

func newDockerClient() (*dockerclient.Client, error) {
	opts := []dockerclient.Opt{dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation()}
	socket := strings.TrimSpace(os.Getenv("WORKSPACE_DOCKER_SOCKET"))
	if socket != "" {
		opts = append(opts, dockerclient.WithHost("unix://"+socket))
	}
	return dockerclient.NewClientWithOpts(opts...)
}

func buildDockerManager(sessionStore app.SessionStore) (app.WorkspaceManager, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	imageBundles, err := parseStringMapEnv(os.Getenv("WORKSPACE_DOCKER_IMAGE_BUNDLES_JSON"))
	if err != nil {
		return nil, fmt.Errorf("parse WORKSPACE_DOCKER_IMAGE_BUNDLES_JSON: %w", err)
	}
	return workspaceadapter.NewDockerManager(workspaceadapter.DockerManagerConfig{
		DefaultImage:    envOrDefault("WORKSPACE_DOCKER_IMAGE", "alpine:3.20"),
		ImageBundles:    imageBundles,
		ProfileKey:      envOrDefault("WORKSPACE_DOCKER_RUNNER_PROFILE_KEY", "runner_profile"),
		Workdir:         envOrDefault("WORKSPACE_DOCKER_WORKDIR", "/workspace/repo"),
		ContainerPrefix: envOrDefault("WORKSPACE_DOCKER_CONTAINER_PREFIX", "ws"),
		Network:         strings.TrimSpace(os.Getenv("WORKSPACE_DOCKER_NETWORK")),
		CPULimit:        int64(parseIntOrDefault(os.Getenv("WORKSPACE_DOCKER_CPU_LIMIT"), 2)),
		MemoryLimit:     int64(parseIntOrDefault(os.Getenv("WORKSPACE_DOCKER_MEMORY_LIMIT_MB"), 2048)) * 1024 * 1024,
		TTL:             time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_DOCKER_TTL_SECONDS"), 3600)) * time.Second,
		SessionStore:    sessionStore,
	}, client), nil
}

func buildDockerCommandRunner() (app.CommandRunner, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return tooladapter.NewDockerCommandRunner(client), nil
}

func dockerClientOrNil(backend string) any {
	if backend != workspaceBackendDocker {
		return nil
	}
	client, err := newDockerClient()
	if err != nil {
		return nil
	}
	return client
}

func parseDisabledBundles() []string {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_DISABLED_BUNDLES"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			result = append(result, v)
		}
	}
	return result
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
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

func buildInvocationStore(logger *slog.Logger, valkeyTLS *tls.Config) (app.InvocationStore, error) {
	backend := strings.ToLower(strings.TrimSpace(envOrDefault("INVOCATION_STORE_BACKEND", "memory")))
	switch backend {
	case "", "memory":
		logger.Info("invocation store initialized", "backend", "memory")
		return app.NewInMemoryInvocationStore(), nil
	case "valkey":
		address := strings.TrimSpace(os.Getenv("VALKEY_ADDR"))
		if address == "" {
			host := strings.TrimSpace(envOrDefault("VALKEY_HOST", "localhost"))
			port := strings.TrimSpace(envOrDefault("VALKEY_PORT", "6379"))
			address = fmt.Sprintf("%s:%s", host, port)
		}

		password := os.Getenv("VALKEY_PASSWORD")
		db := parseIntOrDefault(os.Getenv("VALKEY_DB"), 0)
		keyPrefix := envOrDefault("INVOCATION_STORE_KEY_PREFIX", "workspace:invocation")
		ttlSeconds := parseIntOrDefault(os.Getenv("INVOCATION_STORE_TTL_SECONDS"), 86400)

		store, err := invocationstoreadapter.NewValkeyStoreFromAddress(
			context.Background(),
			address,
			password,
			db,
			keyPrefix,
			time.Duration(ttlSeconds)*time.Second,
			valkeyTLS,
		)
		if err != nil {
			return nil, err
		}
		logger.Info("invocation store initialized", "backend", "valkey", "address", address, "db", db, "ttl_seconds", ttlSeconds)
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported INVOCATION_STORE_BACKEND: %s", backend)
	}
}

func buildSessionStore(logger *slog.Logger, valkeyTLS *tls.Config) (app.SessionStore, error) {
	backend := strings.ToLower(strings.TrimSpace(envOrDefault("SESSION_STORE_BACKEND", "memory")))
	switch backend {
	case "", "memory":
		logger.Info("session store initialized", "backend", "memory")
		return app.NewInMemorySessionStore(), nil
	case "valkey":
		address := strings.TrimSpace(os.Getenv("VALKEY_ADDR"))
		if address == "" {
			host := strings.TrimSpace(envOrDefault("VALKEY_HOST", "localhost"))
			port := strings.TrimSpace(envOrDefault("VALKEY_PORT", "6379"))
			address = fmt.Sprintf("%s:%s", host, port)
		}

		password := os.Getenv("VALKEY_PASSWORD")
		db := parseIntOrDefault(os.Getenv("VALKEY_DB"), 0)
		keyPrefix := envOrDefault("SESSION_STORE_KEY_PREFIX", "workspace:session")
		ttlSeconds := parseIntOrDefault(os.Getenv("SESSION_STORE_TTL_SECONDS"), 86400)

		store, err := sessionstoreadapter.NewValkeyStoreFromAddress(
			context.Background(),
			address,
			password,
			db,
			keyPrefix,
			time.Duration(ttlSeconds)*time.Second,
			valkeyTLS,
		)
		if err != nil {
			return nil, err
		}
		logger.Info("session store initialized", "backend", "valkey", "address", address, "db", db, "ttl_seconds", ttlSeconds)
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported SESSION_STORE_BACKEND: %s", backend)
	}
}

func parseIntOrDefault(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseBoolOrDefault(raw string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

// buildServerTLS builds the TLS configuration for the HTTP server.
// Reads WORKSPACE_TLS_MODE, WORKSPACE_TLS_CERT_PATH, WORKSPACE_TLS_KEY_PATH,
// WORKSPACE_TLS_CLIENT_CA_PATH.
func buildServerTLS(logger *slog.Logger) (*tls.Config, error) {
	mode, err := tlsutil.ParseMode(strings.TrimSpace(os.Getenv("WORKSPACE_TLS_MODE")))
	if err != nil {
		return nil, err
	}
	if mode == tlsutil.ModeDisabled {
		return nil, nil
	}
	cfg, err := tlsutil.BuildServerTLSConfig(tlsutil.Config{
		Mode:     mode,
		CertPath: strings.TrimSpace(os.Getenv("WORKSPACE_TLS_CERT_PATH")),
		KeyPath:  strings.TrimSpace(os.Getenv("WORKSPACE_TLS_KEY_PATH")),
		CAPath:   strings.TrimSpace(os.Getenv("WORKSPACE_TLS_CLIENT_CA_PATH")),
	})
	if err != nil {
		return nil, err
	}
	logger.Info("HTTP server TLS configured", "mode", string(mode))
	return cfg, nil
}

// buildValkeyTLS builds the TLS configuration for all Valkey connections.
// Reads VALKEY_TLS_ENABLED, VALKEY_TLS_CA_PATH, VALKEY_TLS_CERT_PATH,
// VALKEY_TLS_KEY_PATH, VALKEY_TLS_SERVER_NAME.
// NOTE: The kernel uses URI scheme (rediss://) with query params. The Go
// runtime uses separate env vars because go-redis accepts addr + TLSConfig.
// This is a documented divergence from the kernel's Valkey TLS model.
func buildValkeyTLS(logger *slog.Logger) (*tls.Config, error) {
	if !parseBoolOrDefault(os.Getenv("VALKEY_TLS_ENABLED"), false) {
		return nil, nil
	}
	caPath := strings.TrimSpace(os.Getenv("VALKEY_TLS_CA_PATH"))
	certPath := strings.TrimSpace(os.Getenv("VALKEY_TLS_CERT_PATH"))
	keyPath := strings.TrimSpace(os.Getenv("VALKEY_TLS_KEY_PATH"))

	mode := tlsutil.ModeServer
	if certPath != "" && keyPath != "" {
		mode = tlsutil.ModeMutual
	}
	cfg, err := tlsutil.BuildClientTLSConfig(tlsutil.Config{
		Mode:       mode,
		CAPath:     caPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
		ServerName: strings.TrimSpace(os.Getenv("VALKEY_TLS_SERVER_NAME")),
	})
	if err != nil {
		return nil, fmt.Errorf("valkey TLS: %w", err)
	}
	logger.Info("Valkey TLS configured", "mode", string(mode))
	return cfg, nil
}

// buildNATSTLS builds the TLS configuration for the NATS connection.
// Reads NATS_TLS_MODE, NATS_TLS_CA_PATH, NATS_TLS_CERT_PATH,
// NATS_TLS_KEY_PATH, NATS_TLS_SERVER_NAME, NATS_TLS_FIRST.
// NOTE: NATS_TLS_FIRST is read for env-var consistency with the kernel (Rust),
// but the Go NATS client (nats.go) does not support a TLS-first handshake.
// If the NATS server requires TLS-first, the connection will fail.
func buildNATSTLS(logger *slog.Logger) (*tls.Config, error) {
	mode, err := tlsutil.ParseMode(strings.TrimSpace(os.Getenv("NATS_TLS_MODE")))
	if err != nil {
		return nil, err
	}
	if mode == tlsutil.ModeDisabled {
		return nil, nil
	}
	if parseBoolOrDefault(os.Getenv("NATS_TLS_FIRST"), false) {
		logger.Warn("NATS_TLS_FIRST=true requested but Go nats.go client does not support TLS-first handshake; flag ignored")
	}
	cfg, err := tlsutil.BuildClientTLSConfig(tlsutil.Config{
		Mode:       mode,
		CAPath:     strings.TrimSpace(os.Getenv("NATS_TLS_CA_PATH")),
		CertPath:   strings.TrimSpace(os.Getenv("NATS_TLS_CERT_PATH")),
		KeyPath:    strings.TrimSpace(os.Getenv("NATS_TLS_KEY_PATH")),
		ServerName: strings.TrimSpace(os.Getenv("NATS_TLS_SERVER_NAME")),
	})
	if err != nil {
		return nil, fmt.Errorf("nats TLS: %w", err)
	}
	logger.Info("NATS TLS configured", "mode", string(mode))
	return cfg, nil
}

func parseStringMapEnv(raw string) (map[string]string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, nil
	}
	parsed := map[string]string{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func setupTelemetry(ctx context.Context, logger *slog.Logger) (func(context.Context) error, error) {
	if !parseBoolOrDefault(os.Getenv("WORKSPACE_OTEL_ENABLED"), false) {
		return func(context.Context) error { return nil }, nil
	}

	options := []otlptracehttp.Option{}
	if endpoint := strings.TrimSpace(os.Getenv("WORKSPACE_OTEL_EXPORTER_OTLP_ENDPOINT")); endpoint != "" {
		options = append(options, otlptracehttp.WithEndpoint(endpoint))
	}
	if parseBoolOrDefault(os.Getenv("WORKSPACE_OTEL_EXPORTER_OTLP_INSECURE"), false) {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if caPath := strings.TrimSpace(os.Getenv("WORKSPACE_OTEL_TLS_CA_PATH")); caPath != "" {
		otlpTLS, tlsErr := tlsutil.BuildClientTLSFromCA(caPath)
		if tlsErr != nil {
			return nil, fmt.Errorf("OTLP TLS: %w", tlsErr)
		}
		if otlpTLS != nil {
			options = append(options, otlptracehttp.WithTLSClientConfig(otlpTLS))
		}
	}

	client := otlptracehttp.NewClient(options...)
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	resources, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			attribute.String("service.name", "workspace"),
			attribute.String("service.version", envOrDefault("WORKSPACE_VERSION", "unknown")),
			attribute.String("deployment.environment", envOrDefault("WORKSPACE_ENV", "unknown")),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build telemetry resources: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resources),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	logger.Info(
		"telemetry enabled",
		"otlp_endpoint", strings.TrimSpace(os.Getenv("WORKSPACE_OTEL_EXPORTER_OTLP_ENDPOINT")),
		"insecure", parseBoolOrDefault(os.Getenv("WORKSPACE_OTEL_EXPORTER_OTLP_INSECURE"), false),
	)
	return tracerProvider.Shutdown, nil
}

func startGRPCServer(srv *grpc.Server, port, workspaceRoot string, serverTLS *tls.Config, logger *slog.Logger) {
	go func() {
		lis, err := net.Listen("tcp", ":"+port)
		if err != nil {
			logger.Error("failed to listen", "port", port, "error", err)
			os.Exit(1)
		}
		if serverTLS != nil {
			logger.Info("workspace gRPC service listening (TLS)", "port", port, "workspace_root", workspaceRoot)
		} else {
			logger.Info("workspace gRPC service listening", "port", port, "workspace_root", workspaceRoot)
		}
		if err := srv.Serve(lis); err != nil {
			logger.Error("grpc server failed", "error", err)
			os.Exit(1)
		}
	}()
}

// buildEventBus constructs the event publisher stack based on EVENT_BUS env var.
// Returns the publisher, optional NATS connection (for cleanup), and optional
// relay stop function.
//
// EVENT_BUS=none  → NoopPublisher (default)
// EVENT_BUS=nats  → OutboxPublisher → OutboxRelay → NATSPublisher
func buildEventBus(ctx context.Context, logger *slog.Logger, natsTLS *tls.Config, valkeyTLS *tls.Config) (app.EventPublisher, *nats.Conn, func()) {
	bus := strings.ToLower(strings.TrimSpace(envOrDefault("EVENT_BUS", "none")))
	switch bus {
	case "nats":
		natsPub, nc, err := eventbus.NewNATSPublisherFromURL(
			ctx,
			envOrDefault("EVENT_BUS_NATS_URL", "nats://localhost:4222"),
			envOrDefault("EVENT_BUS_NATS_STREAM", ""),
			natsTLS,
		)
		if err != nil {
			logger.Error("failed to connect to NATS event bus, falling back to noop", "error", err)
			return eventbus.NewNoopPublisher(logger), nil, nil
		}

		// Outbox backed by Valkey if available, otherwise use NATS directly.
		outboxPub, outboxRelay := buildOutboxRelay(ctx, logger, natsPub, valkeyTLS)
		if outboxPub != nil {
			logger.Info("event bus initialized", "bus", "nats+outbox")
			return outboxPub, nc, outboxRelay
		}

		logger.Info("event bus initialized", "bus", "nats")
		return natsPub, nc, nil

	default:
		logger.Info("event bus initialized", "bus", "noop")
		return eventbus.NewNoopPublisher(logger), nil, nil
	}
}

// buildOutboxRelay creates an outbox-backed publisher with a relay goroutine
// that forwards events to the downstream publisher. Returns nil if Valkey
// outbox is not configured.
func buildOutboxRelay(ctx context.Context, logger *slog.Logger, downstream app.EventPublisher, valkeyTLS *tls.Config) (app.EventPublisher, func()) {
	if strings.ToLower(strings.TrimSpace(envOrDefault("EVENT_BUS_OUTBOX", "false"))) != "true" {
		return nil, nil
	}

	address := strings.TrimSpace(os.Getenv("VALKEY_ADDR"))
	if address == "" {
		host := strings.TrimSpace(envOrDefault("VALKEY_HOST", "localhost"))
		port := strings.TrimSpace(envOrDefault("VALKEY_PORT", "6379"))
		address = fmt.Sprintf("%s:%s", host, port)
	}
	password := os.Getenv("VALKEY_PASSWORD")
	db := parseIntOrDefault(os.Getenv("VALKEY_DB"), 0)
	keyPrefix := envOrDefault("EVENT_BUS_OUTBOX_KEY_PREFIX", "workspace:outbox")

	outbox, err := eventbus.NewOutboxPublisherFromAddress(ctx, address, password, db, keyPrefix, valkeyTLS)
	if err != nil {
		logger.Warn("outbox valkey connection failed, skipping outbox relay", "error", err)
		return nil, nil
	}

	relay := eventbus.NewOutboxRelay(outbox, downstream, logger)
	relay.Start()
	return outbox, relay.Stop
}

// buildTelemetry creates the telemetry recorder and querier based on
// TELEMETRY_BACKEND env var.
//
// TELEMETRY_BACKEND=none   → NoopRecorder + InMemoryAggregator (default)
// TELEMETRY_BACKEND=memory → InMemoryAggregator (records + queries)
// TELEMETRY_BACKEND=valkey → ValkeyRecorder + Aggregator (background loop)
func buildTelemetry(ctx context.Context, logger *slog.Logger, valkeyTLS *tls.Config) (app.TelemetryRecorder, app.TelemetryQuerier, func()) {
	backend := strings.ToLower(strings.TrimSpace(envOrDefault("TELEMETRY_BACKEND", "none")))
	switch backend {
	case "memory":
		agg := telemetryadapter.NewInMemoryAggregator()
		logger.Info("telemetry initialized", "backend", "memory")
		return agg, agg, nil

	case "valkey":
		address := strings.TrimSpace(os.Getenv("VALKEY_ADDR"))
		if address == "" {
			host := strings.TrimSpace(envOrDefault("VALKEY_HOST", "localhost"))
			port := strings.TrimSpace(envOrDefault("VALKEY_PORT", "6379"))
			address = fmt.Sprintf("%s:%s", host, port)
		}
		password := os.Getenv("VALKEY_PASSWORD")
		db := parseIntOrDefault(os.Getenv("VALKEY_DB"), 0)
		keyPrefix := envOrDefault("TELEMETRY_KEY_PREFIX", "workspace:telemetry")
		ttlSeconds := parseIntOrDefault(os.Getenv("TELEMETRY_TTL_SECONDS"), 604800) // 7 days

		rec, err := telemetryadapter.NewValkeyRecorderFromAddress(
			ctx, address, password, db, keyPrefix, time.Duration(ttlSeconds)*time.Second, valkeyTLS,
		)
		if err != nil {
			logger.Warn("telemetry valkey connection failed, falling back to memory", "error", err)
			agg := telemetryadapter.NewInMemoryAggregator()
			return agg, agg, nil
		}

		intervalSec := parseIntOrDefault(os.Getenv("TELEMETRY_AGGREGATION_INTERVAL_SECONDS"), 300)
		agg := telemetryadapter.NewAggregator(rec, logger,
			telemetryadapter.WithAggregationInterval(time.Duration(intervalSec)*time.Second),
		)
		agg.Start()
		logger.Info("telemetry initialized", "backend", "valkey", "address", address, "interval_seconds", intervalSec)
		return rec, agg, agg.Stop

	default:
		logger.Info("telemetry initialized", "backend", "noop")
		agg := telemetryadapter.NewInMemoryAggregator()
		return telemetryadapter.NoopRecorder{}, agg, nil
	}
}

// buildArtifactStore creates the artifact store based on ARTIFACT_BACKEND env var.
//
// ARTIFACT_BACKEND=local → LocalArtifactStore (default)
// ARTIFACT_BACKEND=s3    → S3ArtifactStore (MinIO or AWS S3)
func buildArtifactStore(ctx context.Context, localRoot string, logger *slog.Logger) (app.ArtifactStore, error) {
	backend := strings.ToLower(strings.TrimSpace(envOrDefault("ARTIFACT_BACKEND", "local")))
	switch backend {
	case "", "local":
		logger.Info("artifact store initialized", "backend", "local", "root", localRoot)
		return storage.NewLocalArtifactStore(localRoot), nil
	case "s3":
		useSSL := parseBoolOrDefault(os.Getenv("ARTIFACT_S3_USE_SSL"), false)
		var s3TLS *tls.Config
		caPath := strings.TrimSpace(os.Getenv("ARTIFACT_S3_CA_PATH"))
		certPath := strings.TrimSpace(os.Getenv("ARTIFACT_S3_CERT_PATH"))
		keyPath := strings.TrimSpace(os.Getenv("ARTIFACT_S3_KEY_PATH"))
		if caPath != "" || (certPath != "" && keyPath != "") {
			mode := tlsutil.ModeServer
			if certPath != "" && keyPath != "" {
				mode = tlsutil.ModeMutual
			}
			var tlsErr error
			s3TLS, tlsErr = tlsutil.BuildClientTLSConfig(tlsutil.Config{
				Mode:     mode,
				CAPath:   caPath,
				CertPath: certPath,
				KeyPath:  keyPath,
			})
			if tlsErr != nil {
				return nil, fmt.Errorf("s3 TLS: %w", tlsErr)
			}
		}
		cfg := storage.S3Config{
			Bucket:    envOrDefault("ARTIFACT_S3_BUCKET", "workspace-artifacts"),
			Prefix:    strings.TrimSpace(os.Getenv("ARTIFACT_S3_PREFIX")),
			Endpoint:  strings.TrimSpace(os.Getenv("ARTIFACT_S3_ENDPOINT")),
			Region:    envOrDefault("ARTIFACT_S3_REGION", "us-east-1"),
			AccessKey: strings.TrimSpace(os.Getenv("ARTIFACT_S3_ACCESS_KEY")),
			SecretKey: os.Getenv("ARTIFACT_S3_SECRET_KEY"),
			PathStyle: parseBoolOrDefault(os.Getenv("ARTIFACT_S3_PATH_STYLE"), true),
			UseSSL:    useSSL,
			TLSConfig: s3TLS,
		}
		store, err := storage.NewS3ArtifactStoreFromConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("s3 artifact store: %w", err)
		}
		logger.Info("artifact store initialized", "backend", "s3",
			"bucket", cfg.Bucket, "region", cfg.Region, "endpoint", cfg.Endpoint)
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported ARTIFACT_BACKEND: %s", backend)
	}
}
