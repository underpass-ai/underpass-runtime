package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/adapters/audit"
	invocationstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/invocationstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/policy"
	sessionstoreadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/sessionstore"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/httpapi"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	workspaceBackendLocal   = "local"
	defaultNamespace        = "underpass-runtime"
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

	var kubeConfig *rest.Config
	var kubeClient kubernetes.Interface
	if workspaceBackend == "kubernetes" {
		resolvedConfig, resolvedClient, err := buildKubernetesClient()
		if err != nil {
			logger.Error("failed to initialize kubernetes client", "error", err)
			os.Exit(1)
		}
		kubeConfig = resolvedConfig
		kubeClient = resolvedClient
	}

	sessionStore, err := buildSessionStore(logger)
	if err != nil {
		logger.Error("failed to initialize session store", "error", err)
		os.Exit(1)
	}

	workspaceManager, err := buildWorkspaceManager(workspaceBackend, workspaceRoot, kubeClient, sessionStore)
	if err != nil {
		logger.Error("failed to initialize workspace manager", "error", err)
		os.Exit(1)
	}
	podJanitorCancel := startPodJanitorIfEnabled(workspaceBackend, workspaceNamespace, kubeClient, sessionStore, logger)
	catalog := tooladapter.NewCatalog(tooladapter.DefaultCapabilities())
	commandRunner, err := buildCommandRunner(workspaceBackend, kubeClient, kubeConfig)
	if err != nil {
		logger.Error("failed to initialize command runner", "error", err)
		os.Exit(1)
	}
	engine := tooladapter.NewEngine(
		tooladapter.NewFSListHandler(commandRunner),
		tooladapter.NewFSReadHandler(commandRunner),
		tooladapter.NewFSWriteHandler(commandRunner),
		tooladapter.NewFSMkdirHandler(commandRunner),
		tooladapter.NewFSMoveHandler(commandRunner),
		tooladapter.NewFSCopyHandler(commandRunner),
		tooladapter.NewFSDeleteHandler(commandRunner),
		tooladapter.NewFSStatHandler(commandRunner),
		tooladapter.NewFSPatchHandler(commandRunner),
		tooladapter.NewFSSearchHandler(commandRunner),
		tooladapter.NewConnListProfilesHandler(),
		tooladapter.NewConnDescribeProfileHandler(),
		tooladapter.NewAPIBenchmarkHandler(commandRunner),
		tooladapter.NewNATSRequestHandler(nil),
		tooladapter.NewNATSPublishHandler(nil),
		tooladapter.NewNATSSubscribePullHandler(nil),
		tooladapter.NewKafkaConsumeHandler(nil),
		tooladapter.NewKafkaProduceHandler(nil),
		tooladapter.NewKafkaTopicMetadataHandler(nil),
		tooladapter.NewRabbitConsumeHandler(nil),
		tooladapter.NewRabbitPublishHandler(nil),
		tooladapter.NewRabbitQueueInfoHandler(nil),
		tooladapter.NewRedisGetHandler(nil),
		tooladapter.NewRedisMGetHandler(nil),
		tooladapter.NewRedisScanHandler(nil),
		tooladapter.NewRedisTTLHandler(nil),
		tooladapter.NewRedisExistsHandler(nil),
		tooladapter.NewRedisSetHandler(nil),
		tooladapter.NewRedisDelHandler(nil),
		tooladapter.NewMongoFindHandler(nil),
		tooladapter.NewMongoAggregateHandler(nil),
		tooladapter.NewGitStatusHandler(commandRunner),
		tooladapter.NewGitDiffHandler(commandRunner),
		tooladapter.NewGitApplyPatchHandler(commandRunner),
		tooladapter.NewGitCheckoutHandler(commandRunner),
		tooladapter.NewGitLogHandler(commandRunner),
		tooladapter.NewGitShowHandler(commandRunner),
		tooladapter.NewGitBranchListHandler(commandRunner),
		tooladapter.NewGitCommitHandler(commandRunner),
		tooladapter.NewGitPushHandler(commandRunner),
		tooladapter.NewGitFetchHandler(commandRunner),
		tooladapter.NewGitPullHandler(commandRunner),
		tooladapter.NewRepoDetectProjectTypeHandler(commandRunner),
		tooladapter.NewRepoDetectToolchainHandler(commandRunner),
		tooladapter.NewRepoValidateHandler(commandRunner),
		tooladapter.NewRepoBuildHandler(commandRunner),
		tooladapter.NewRepoTestHandler(commandRunner),
		tooladapter.NewRepoRunTestsHandler(commandRunner),
		tooladapter.NewRepoTestFailuresSummaryHandler(commandRunner),
		tooladapter.NewRepoStacktraceSummaryHandler(commandRunner),
		tooladapter.NewRepoChangedFilesHandler(commandRunner),
		tooladapter.NewRepoSymbolSearchHandler(commandRunner),
		tooladapter.NewRepoFindReferencesHandler(commandRunner),
		tooladapter.NewRepoCoverageReportHandler(commandRunner),
		tooladapter.NewRepoStaticAnalysisHandler(commandRunner),
		tooladapter.NewRepoPackageHandler(commandRunner),
		tooladapter.NewArtifactUploadHandler(commandRunner),
		tooladapter.NewArtifactDownloadHandler(commandRunner),
		tooladapter.NewArtifactListHandler(commandRunner),
		tooladapter.NewImageBuildHandler(commandRunner),
		tooladapter.NewImagePushHandler(commandRunner),
		tooladapter.NewImageInspectHandler(commandRunner),
		tooladapter.NewContainerPSHandlerWithKubernetes(commandRunner, kubeClient, workspaceNamespace),
		tooladapter.NewContainerLogsHandlerWithKubernetes(commandRunner, kubeClient, workspaceNamespace),
		tooladapter.NewContainerRunHandlerWithKubernetes(commandRunner, kubeClient, workspaceNamespace),
		tooladapter.NewContainerExecHandlerWithKubernetes(commandRunner, kubeClient, workspaceNamespace),
		tooladapter.NewK8sGetPodsHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sGetServicesHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sGetDeploymentsHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sGetImagesHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sGetLogsHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sApplyManifestHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sRolloutStatusHandler(kubeClient, workspaceNamespace),
		tooladapter.NewK8sRestartDeploymentHandler(kubeClient, workspaceNamespace),
		tooladapter.NewSecurityScanDependenciesHandler(commandRunner),
		tooladapter.NewSBOMGenerateHandler(commandRunner),
		tooladapter.NewSecurityScanSecretsHandler(commandRunner),
		tooladapter.NewSecurityScanContainerHandler(commandRunner),
		tooladapter.NewSecurityLicenseCheckHandler(commandRunner),
		tooladapter.NewQualityGateHandler(commandRunner),
		tooladapter.NewCIRunPipelineHandler(commandRunner),
		tooladapter.NewGoModTidyHandler(commandRunner),
		tooladapter.NewGoGenerateHandler(commandRunner),
		tooladapter.NewGoBuildHandler(commandRunner),
		tooladapter.NewGoTestHandler(commandRunner),
		tooladapter.NewRustBuildHandler(commandRunner),
		tooladapter.NewRustTestHandler(commandRunner),
		tooladapter.NewRustClippyHandler(commandRunner),
		tooladapter.NewRustFormatHandler(commandRunner),
		tooladapter.NewNodeInstallHandler(commandRunner),
		tooladapter.NewNodeBuildHandler(commandRunner),
		tooladapter.NewNodeTestHandler(commandRunner),
		tooladapter.NewNodeLintHandler(commandRunner),
		tooladapter.NewNodeTypecheckHandler(commandRunner),
		tooladapter.NewPythonInstallDepsHandler(commandRunner),
		tooladapter.NewPythonValidateHandler(commandRunner),
		tooladapter.NewPythonTestHandler(commandRunner),
		tooladapter.NewCBuildHandler(commandRunner),
		tooladapter.NewCTestHandler(commandRunner),
	)
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

func buildWorkspaceManager(
	backend string,
	workspaceRoot string,
	kubeClient kubernetes.Interface,
	sessionStore app.SessionStore,
) (app.WorkspaceManager, error) {
	switch backend {
	case "", workspaceBackendLocal:
		if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create workspace root: %w", err)
		}
		return workspaceadapter.NewLocalManager(workspaceRoot), nil
	case "kubernetes":
		if kubeClient == nil {
			return nil, fmt.Errorf("kubernetes client is required")
		}
		runnerImageBundles, err := parseStringMapEnv(os.Getenv("WORKSPACE_K8S_RUNNER_IMAGE_BUNDLES_JSON"))
		if err != nil {
			return nil, fmt.Errorf("parse WORKSPACE_K8S_RUNNER_IMAGE_BUNDLES_JSON: %w", err)
		}
		return workspaceadapter.NewKubernetesManager(workspaceadapter.KubernetesManagerConfig{
			Namespace:           envOrDefault("WORKSPACE_K8S_NAMESPACE", defaultNamespace),
			ServiceAccount:      strings.TrimSpace(os.Getenv("WORKSPACE_K8S_SERVICE_ACCOUNT")),
			PodImage:            envOrDefault("WORKSPACE_K8S_RUNNER_IMAGE", ""),
			RunnerImageBundles:  runnerImageBundles,
			RunnerProfileKey:    envOrDefault("WORKSPACE_K8S_RUNNER_PROFILE_METADATA_KEY", "runner_profile"),
			InitImage:           envOrDefault("WORKSPACE_K8S_INIT_IMAGE", ""),
			WorkspaceDir:        envOrDefault("WORKSPACE_K8S_WORKDIR", "/workspace/repo"),
			RunnerContainerName: envOrDefault("WORKSPACE_K8S_CONTAINER", "runner"),
			PodNamePrefix:       envOrDefault("WORKSPACE_K8S_POD_PREFIX", "ws"),
			PodReadyTimeout:     time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_READY_TIMEOUT_SECONDS"), 120)) * time.Second,
			SessionStore:        sessionStore,
			GitAuthSecretName:   strings.TrimSpace(os.Getenv("WORKSPACE_K8S_GIT_AUTH_SECRET")),
			GitAuthMetadataKey:  envOrDefault("WORKSPACE_K8S_GIT_AUTH_METADATA_KEY", "git_auth_secret"),
			RunAsUser:           int64(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_RUN_AS_USER"), 1000)),
			RunAsGroup:          int64(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_RUN_AS_GROUP"), 1000)),
			FSGroup:             int64(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_FS_GROUP"), 1000)),
			ReadOnlyRootFS:      parseBoolOrDefault(os.Getenv("WORKSPACE_K8S_READ_ONLY_ROOT_FS"), false),
			AutomountSAToken:    parseBoolOrDefault(os.Getenv("WORKSPACE_K8S_AUTOMOUNT_SA_TOKEN"), false),
		}, kubeClient), nil
	default:
		return nil, fmt.Errorf("unsupported WORKSPACE_BACKEND: %s", backend)
	}
}

func buildCommandRunner(
	backend string,
	kubeClient kubernetes.Interface,
	kubeConfig *rest.Config,
) (app.CommandRunner, error) {
	localRunner := tooladapter.NewLocalCommandRunner()
	if backend != "kubernetes" {
		return localRunner, nil
	}
	if kubeClient == nil || kubeConfig == nil {
		return nil, fmt.Errorf("kubernetes runner requires client and rest config")
	}
	k8sRunner := tooladapter.NewK8sCommandRunner(
		kubeClient,
		kubeConfig,
		envOrDefault("WORKSPACE_K8S_NAMESPACE", defaultNamespace),
	)
	return tooladapter.NewRoutingCommandRunner(localRunner, k8sRunner), nil
}

func buildKubernetesClient() (*rest.Config, kubernetes.Interface, error) {
	kubeConfig, err := resolveKubeConfig()
	if err != nil {
		return nil, nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, err
	}
	return kubeConfig, clientset, nil
}

func resolveKubeConfig() (*rest.Config, error) {
	if kubeconfig := strings.TrimSpace(os.Getenv("KUBECONFIG")); kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		defaultKubeconfig := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultKubeconfig); err == nil {
			return clientcmd.BuildConfigFromFlags("", defaultKubeconfig)
		}
	}
	return rest.InClusterConfig()
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

func startPodJanitorIfEnabled(
	workspaceBackend, workspaceNamespace string,
	kubeClient kubernetes.Interface,
	sessionStore app.SessionStore,
	logger *slog.Logger,
) context.CancelFunc {
	if workspaceBackend != "kubernetes" || kubeClient == nil || !parseBoolOrDefault(os.Getenv("WORKSPACE_K8S_POD_JANITOR_ENABLED"), true) {
		return nil
	}
	interval := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_POD_JANITOR_INTERVAL_SECONDS"), 60)) * time.Second
	sessionPodTTL := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_SESSION_POD_TERMINAL_TTL_SECONDS"), 300)) * time.Second
	containerPodTTL := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_CONTAINER_POD_TERMINAL_TTL_SECONDS"), 300)) * time.Second
	missingSessionGrace := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_MISSING_SESSION_GRACE_SECONDS"), 120)) * time.Second

	janitor := workspaceadapter.NewKubernetesPodJanitor(kubeClient, workspaceadapter.KubernetesPodJanitorConfig{
		Namespace:                 workspaceNamespace,
		SessionStore:              sessionStore,
		Interval:                  interval,
		SessionTerminalPodTTL:     sessionPodTTL,
		ContainerTerminalPodTTL:   containerPodTTL,
		MissingSessionGracePeriod: missingSessionGrace,
		Logger:                    logger.With("component", "k8s-pod-janitor"),
	})
	janitorCtx, cancel := context.WithCancel(context.Background())
	go janitor.Start(janitorCtx)
	logger.Info(
		"kubernetes pod janitor enabled",
		"namespace", workspaceNamespace,
		"interval_seconds", int(interval/time.Second),
		"session_terminal_ttl_seconds", int(sessionPodTTL/time.Second),
		"container_terminal_ttl_seconds", int(containerPodTTL/time.Second),
		"missing_session_grace_seconds", int(missingSessionGrace/time.Second),
	)
	return cancel
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
