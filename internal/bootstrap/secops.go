package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// SecopsBundle returns security scanning, SBOM, quality gate, and CI handlers.
func SecopsBundle() Bundle {
	return Bundle{
		Name: "secops",
		Build: func(cfg Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewSecurityScanDependenciesHandler(cfg.CommandRunner),
				tooladapter.NewSBOMGenerateHandler(cfg.CommandRunner),
				tooladapter.NewSecurityScanSecretsHandler(cfg.CommandRunner),
				tooladapter.NewSecurityScanContainerHandler(cfg.CommandRunner),
				tooladapter.NewSecurityLicenseCheckHandler(cfg.CommandRunner),
				tooladapter.NewQualityGateHandler(cfg.CommandRunner),
				tooladapter.NewCIRunPipelineHandler(cfg.CommandRunner),
			}
		},
	}
}
