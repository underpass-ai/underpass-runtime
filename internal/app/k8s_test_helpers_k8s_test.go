//go:build k8s

package app_test

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

func k8sToolHandlers() []tooladapter.Handler {
	return []tooladapter.Handler{
		tooladapter.NewK8sGetPodsHandler(nil, "default"),
		tooladapter.NewK8sGetServicesHandler(nil, "default"),
		tooladapter.NewK8sGetDeploymentsHandler(nil, "default"),
		tooladapter.NewK8sGetImagesHandler(nil, "default"),
		tooladapter.NewK8sGetLogsHandler(nil, "default"),
		tooladapter.NewK8sApplyManifestHandler(nil, "default"),
		tooladapter.NewK8sRolloutStatusHandler(nil, "default"),
		tooladapter.NewK8sRestartDeploymentHandler(nil, "default"),
		tooladapter.NewK8sScaleDeploymentHandler(nil, "default"),
		tooladapter.NewK8sRestartPodsHandler(nil, "default"),
		tooladapter.NewK8sCircuitBreakHandler(nil, "default"),
		tooladapter.NewK8sSetImageHandler(nil, "default"),
	}
}
