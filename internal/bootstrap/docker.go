package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
)

// DockerBundle returns container tool handlers backed by the Docker Engine API.
// The Config.DockerClient must be a workspace.DockerClient.
func DockerBundle() Bundle {
	return Bundle{
		Name: "docker",
		Build: func(cfg Config) []tooladapter.Handler {
			client, ok := cfg.DockerClient.(workspaceadapter.DockerClient)
			if !ok || client == nil {
				return nil
			}
			return []tooladapter.Handler{
				tooladapter.NewContainerPSHandlerWithDocker(cfg.CommandRunner, client),
				tooladapter.NewContainerLogsHandlerWithDocker(cfg.CommandRunner, client),
				tooladapter.NewContainerRunHandlerWithDocker(cfg.CommandRunner, client),
				tooladapter.NewContainerExecHandlerWithDocker(cfg.CommandRunner, client),
			}
		},
	}
}
