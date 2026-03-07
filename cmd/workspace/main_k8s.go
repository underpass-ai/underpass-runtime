//go:build k8s

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/bootstrap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
)

type k8sRuntime struct {
	config *rest.Config
	client kubernetes.Interface
}

func initKubernetesRuntime(backend string) (*k8sRuntime, error) {
	if backend != "kubernetes" {
		return nil, nil
	}
	kubeConfig, err := resolveKubeConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}
	return &k8sRuntime{config: kubeConfig, client: clientset}, nil
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

func buildWorkspaceManager(
	backend string,
	workspaceRoot string,
	k8s *k8sRuntime,
	sessionStore app.SessionStore,
) (app.WorkspaceManager, error) {
	switch backend {
	case "", workspaceBackendLocal:
		if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create workspace root: %w", err)
		}
		return workspaceadapter.NewLocalManager(workspaceRoot), nil
	case "kubernetes":
		if k8s == nil || k8s.client == nil {
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
		}, k8s.client), nil
	default:
		return nil, fmt.Errorf("unsupported WORKSPACE_BACKEND: %s", backend)
	}
}

func buildCommandRunner(backend string, k8s *k8sRuntime) (app.CommandRunner, error) {
	localRunner := tooladapter.NewLocalCommandRunner()
	if backend != "kubernetes" {
		return localRunner, nil
	}
	if k8s == nil || k8s.client == nil || k8s.config == nil {
		return nil, fmt.Errorf("kubernetes runner requires client and rest config")
	}
	k8sRunner := tooladapter.NewK8sCommandRunner(
		k8s.client,
		k8s.config,
		envOrDefault("WORKSPACE_K8S_NAMESPACE", defaultNamespace),
	)
	return tooladapter.NewRoutingCommandRunner(localRunner, k8sRunner), nil
}

func startPodJanitorIfEnabled(
	workspaceBackend, workspaceNamespace string,
	k8s *k8sRuntime,
	sessionStore app.SessionStore,
	logger *slog.Logger,
) context.CancelFunc {
	if workspaceBackend != "kubernetes" || k8s == nil || k8s.client == nil || !parseBoolOrDefault(os.Getenv("WORKSPACE_K8S_POD_JANITOR_ENABLED"), true) {
		return nil
	}
	interval := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_POD_JANITOR_INTERVAL_SECONDS"), 60)) * time.Second
	sessionPodTTL := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_SESSION_POD_TERMINAL_TTL_SECONDS"), 300)) * time.Second
	containerPodTTL := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_CONTAINER_POD_TERMINAL_TTL_SECONDS"), 300)) * time.Second
	missingSessionGrace := time.Duration(parseIntOrDefault(os.Getenv("WORKSPACE_K8S_MISSING_SESSION_GRACE_SECONDS"), 120)) * time.Second

	janitor := workspaceadapter.NewKubernetesPodJanitor(k8s.client, workspaceadapter.KubernetesPodJanitorConfig{
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

func buildToolRegistry(k8s *k8sRuntime, _ string) *bootstrap.Registry {
	registry := bootstrap.NewRegistry(parseDisabledBundles()...)
	registry.RegisterDefaults()
	if k8s != nil && k8s.client != nil {
		registry.Register(bootstrap.K8sBundle())
	}
	return registry
}

func k8sClientOrNil(k8s *k8sRuntime) any {
	if k8s == nil || k8s.client == nil {
		return nil
	}
	return k8s.client
}

func k8sToolHandlers(runner app.CommandRunner, k8s *k8sRuntime, namespace string) []tooladapter.Handler {
	if k8s == nil || k8s.client == nil {
		return nil
	}
	return []tooladapter.Handler{
		tooladapter.NewContainerPSHandlerWithKubernetes(runner, k8s.client, namespace),
		tooladapter.NewContainerLogsHandlerWithKubernetes(runner, k8s.client, namespace),
		tooladapter.NewContainerRunHandlerWithKubernetes(runner, k8s.client, namespace),
		tooladapter.NewContainerExecHandlerWithKubernetes(runner, k8s.client, namespace),
		tooladapter.NewK8sGetPodsHandler(k8s.client, namespace),
		tooladapter.NewK8sGetServicesHandler(k8s.client, namespace),
		tooladapter.NewK8sGetDeploymentsHandler(k8s.client, namespace),
		tooladapter.NewK8sGetImagesHandler(k8s.client, namespace),
		tooladapter.NewK8sGetLogsHandler(k8s.client, namespace),
		tooladapter.NewK8sApplyManifestHandler(k8s.client, namespace),
		tooladapter.NewK8sRolloutStatusHandler(k8s.client, namespace),
		tooladapter.NewK8sRestartDeploymentHandler(k8s.client, namespace),
	}
}
