//go:build k8s

package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	"k8s.io/client-go/kubernetes"
)

// K8sBundle returns Kubernetes-specific tool handlers.
// The Config.K8sClient must be a kubernetes.Interface.
func K8sBundle() Bundle {
	return Bundle{
		Name: "k8s",
		Build: func(cfg Config) []tooladapter.Handler {
			client, ok := cfg.K8sClient.(kubernetes.Interface)
			if !ok || client == nil {
				return nil
			}
			ns := cfg.K8sNamespace
			return []tooladapter.Handler{
				tooladapter.NewContainerPSHandlerWithKubernetes(cfg.CommandRunner, client, ns),
				tooladapter.NewContainerLogsHandlerWithKubernetes(cfg.CommandRunner, client, ns),
				tooladapter.NewContainerRunHandlerWithKubernetes(cfg.CommandRunner, client, ns),
				tooladapter.NewContainerExecHandlerWithKubernetes(cfg.CommandRunner, client, ns),
				tooladapter.NewK8sGetPodsHandler(client, ns),
				tooladapter.NewK8sGetServicesHandler(client, ns),
				tooladapter.NewK8sGetDeploymentsHandler(client, ns),
				tooladapter.NewK8sGetReplicaSetsHandler(client, ns),
				tooladapter.NewK8sGetImagesHandler(client, ns),
				tooladapter.NewK8sGetLogsHandler(client, ns),
				tooladapter.NewK8sApplyManifestHandler(client, ns),
				tooladapter.NewK8sRolloutStatusHandler(client, ns),
				tooladapter.NewK8sRolloutPauseHandler(client, ns),
				tooladapter.NewK8sRolloutUndoHandler(client, ns),
				tooladapter.NewK8sRestartDeploymentHandler(client, ns),
				tooladapter.NewK8sScaleDeploymentHandler(client, ns),
				tooladapter.NewK8sRestartPodsHandler(client, ns),
				tooladapter.NewK8sCircuitBreakHandler(client, ns),
				tooladapter.NewK8sSetImageHandler(client, ns),
			}
		},
	}
}
