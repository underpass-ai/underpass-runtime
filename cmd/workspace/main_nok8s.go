//go:build !k8s

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/bootstrap"
)

type k8sRuntime struct{}

func initKubernetesRuntime(backend string) (*k8sRuntime, error) {
	if backend == "kubernetes" {
		return nil, fmt.Errorf("kubernetes backend requires building with -tags k8s")
	}
	return nil, nil
}

func buildWorkspaceManager(
	backend string,
	workspaceRoot string,
	_ *k8sRuntime,
	sessionStore app.SessionStore,
) (app.WorkspaceManager, error) {
	switch backend {
	case "", workspaceBackendLocal:
		if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create workspace root: %w", err)
		}
		return workspaceadapter.NewLocalManager(workspaceRoot), nil
	case workspaceBackendDocker:
		return buildDockerManager(sessionStore)
	default:
		return nil, fmt.Errorf("unsupported WORKSPACE_BACKEND without k8s tag: %s", backend)
	}
}

func buildCommandRunner(backend string, _ *k8sRuntime) (app.CommandRunner, error) {
	if backend == workspaceBackendDocker {
		return buildDockerCommandRunner()
	}
	return tooladapter.NewLocalCommandRunner(), nil
}

func startPodJanitorIfEnabled(_, _ string, _ *k8sRuntime, _ app.SessionStore, _ *slog.Logger) context.CancelFunc {
	return nil
}

func buildToolRegistry(_ *k8sRuntime, _ string) *bootstrap.Registry {
	registry := bootstrap.NewRegistry(parseDisabledBundles()...)
	registry.RegisterDefaults()
	return registry
}

func k8sClientOrNil(_ *k8sRuntime) any {
	return nil
}

func k8sToolHandlers(_ app.CommandRunner, _ *k8sRuntime, _ string) []tooladapter.Handler {
	return nil
}
