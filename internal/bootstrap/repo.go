package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// RepoBundle returns repo analysis, language toolchains, and API benchmark handlers.
func RepoBundle() Bundle {
	return Bundle{
		Name: "repo",
		Build: func(cfg Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewRepoDetectProjectTypeHandler(cfg.CommandRunner),
				tooladapter.NewRepoDetectToolchainHandler(cfg.CommandRunner),
				tooladapter.NewRepoValidateHandler(cfg.CommandRunner),
				tooladapter.NewRepoBuildHandler(cfg.CommandRunner),
				tooladapter.NewRepoTestHandler(cfg.CommandRunner),
				tooladapter.NewRepoRunTestsHandler(cfg.CommandRunner),
				tooladapter.NewRepoTestFailuresSummaryHandler(cfg.CommandRunner),
				tooladapter.NewRepoStacktraceSummaryHandler(cfg.CommandRunner),
				tooladapter.NewRepoChangedFilesHandler(cfg.CommandRunner),
				tooladapter.NewRepoSymbolSearchHandler(cfg.CommandRunner),
				tooladapter.NewRepoFindReferencesHandler(cfg.CommandRunner),
				tooladapter.NewRepoCoverageReportHandler(cfg.CommandRunner),
				tooladapter.NewRepoStaticAnalysisHandler(cfg.CommandRunner),
				tooladapter.NewRepoPackageHandler(cfg.CommandRunner),
				tooladapter.NewAPIBenchmarkHandler(cfg.CommandRunner),
				tooladapter.NewGoModTidyHandler(cfg.CommandRunner),
				tooladapter.NewGoGenerateHandler(cfg.CommandRunner),
				tooladapter.NewGoBuildHandler(cfg.CommandRunner),
				tooladapter.NewGoTestHandler(cfg.CommandRunner),
				tooladapter.NewRustBuildHandler(cfg.CommandRunner),
				tooladapter.NewRustTestHandler(cfg.CommandRunner),
				tooladapter.NewRustClippyHandler(cfg.CommandRunner),
				tooladapter.NewRustFormatHandler(cfg.CommandRunner),
				tooladapter.NewNodeInstallHandler(cfg.CommandRunner),
				tooladapter.NewNodeBuildHandler(cfg.CommandRunner),
				tooladapter.NewNodeTestHandler(cfg.CommandRunner),
				tooladapter.NewNodeLintHandler(cfg.CommandRunner),
				tooladapter.NewNodeTypecheckHandler(cfg.CommandRunner),
				tooladapter.NewPythonInstallDepsHandler(cfg.CommandRunner),
				tooladapter.NewPythonValidateHandler(cfg.CommandRunner),
				tooladapter.NewPythonTestHandler(cfg.CommandRunner),
				tooladapter.NewCBuildHandler(cfg.CommandRunner),
				tooladapter.NewCTestHandler(cfg.CommandRunner),
			}
		},
	}
}
