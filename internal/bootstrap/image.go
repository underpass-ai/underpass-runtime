package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// ImageBundle returns image build/push, container ops, and artifact handlers.
func ImageBundle() Bundle {
	return Bundle{
		Name: "image",
		Build: func(cfg Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewArtifactUploadHandler(cfg.CommandRunner),
				tooladapter.NewArtifactDownloadHandler(cfg.CommandRunner),
				tooladapter.NewArtifactListHandler(cfg.CommandRunner),
				tooladapter.NewImageBuildHandler(cfg.CommandRunner),
				tooladapter.NewImagePushHandler(cfg.CommandRunner),
				tooladapter.NewImageInspectHandler(cfg.CommandRunner),
				tooladapter.NewContainerPSHandler(cfg.CommandRunner),
				tooladapter.NewContainerLogsHandler(cfg.CommandRunner),
				tooladapter.NewContainerRunHandler(cfg.CommandRunner),
				tooladapter.NewContainerExecHandler(cfg.CommandRunner),
			}
		},
	}
}
