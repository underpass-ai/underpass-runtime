package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	dockerclient "github.com/docker/docker/client"

	"github.com/underpass-ai/underpass-runtime/internal/adapters/audit"
	invocationstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/invocationstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/policy"
	sessionstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/sessionstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/bootstrap"
	"github.com/underpass-ai/underpass-runtime/internal/httpapi"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
	workspaceRoot := envOrDefault("WORKSPACE_ROOT", "/tmp/swe-workspaces")
	artifactRoot := envOrDefault("ARTIFACT_ROOT", "/tmp/swe-artifacts")
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

	sessionStore, err := buildSessionStore(logger)
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
	artifactStore := storage.NewLocalArtifactStore(artifactRoot)
	policyEngine := policy.NewStaticPolicy()
	auditLogger := audit.NewLoggerAudit(logger)
	invocationStore, err := buildInvocationStore(logger)
	if err != nil {
		logger.Error("failed to initialize invocation store", "error", err)
		os.Exit(1)
	}

	service := app.NewService(workspaceManager, catalog, policyEngine, engine, artifactStore, auditLogger, invocationStore)
	authConfig, err := httpapi.AuthConfigFromEnv()
	if err != nil {
		logger.Error("failed to initialize auth configuration", "error", err)
		os.Exit(1)
	}
	server := httpapi.NewServer(logger, service, authConfig)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	startHTTPServer(httpServer, port, workspaceRoot, logger)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	if podJanitorCancel != nil {
		podJanitorCancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
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

func buildInvocationStore(logger *slog.Logger) (app.InvocationStore, error) {
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

func buildSessionStore(logger *slog.Logger) (app.SessionStore, error) {
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

func startHTTPServer(srv *http.Server, port, workspaceRoot string, logger *slog.Logger) {
	go func() {
		logger.Info("workspace service listening", "port", port, "workspace_root", workspaceRoot)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()
}
