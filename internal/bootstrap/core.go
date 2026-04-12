package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// CoreBundle returns the core toolset: fs, git, and connection handlers.
func CoreBundle() Bundle {
	return Bundle{
		Name: "core",
		Build: func(cfg Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewFSListHandler(cfg.CommandRunner),
				tooladapter.NewFSReadHandler(cfg.CommandRunner),
				tooladapter.NewFSWriteHandler(cfg.CommandRunner),
				tooladapter.NewFSMkdirHandler(cfg.CommandRunner),
				tooladapter.NewFSMoveHandler(cfg.CommandRunner),
				tooladapter.NewFSCopyHandler(cfg.CommandRunner),
				tooladapter.NewFSDeleteHandler(cfg.CommandRunner),
				tooladapter.NewFSStatHandler(cfg.CommandRunner),
				tooladapter.NewFSEditHandler(cfg.CommandRunner),
				tooladapter.NewFSPatchHandler(cfg.CommandRunner),
				tooladapter.NewFSSearchHandler(cfg.CommandRunner),
				tooladapter.NewConnListProfilesHandler(),
				tooladapter.NewConnDescribeProfileHandler(),
				tooladapter.NewGitStatusHandler(cfg.CommandRunner),
				tooladapter.NewGitDiffHandler(cfg.CommandRunner),
				tooladapter.NewGitApplyPatchHandler(cfg.CommandRunner),
				tooladapter.NewGitCheckoutHandler(cfg.CommandRunner),
				tooladapter.NewGitLogHandler(cfg.CommandRunner),
				tooladapter.NewGitShowHandler(cfg.CommandRunner),
				tooladapter.NewGitBranchListHandler(cfg.CommandRunner),
				tooladapter.NewGitCommitHandler(cfg.CommandRunner),
				tooladapter.NewGitPushHandler(cfg.CommandRunner),
				tooladapter.NewGitFetchHandler(cfg.CommandRunner),
				tooladapter.NewGitPullHandler(cfg.CommandRunner),
			}
		},
	}
}
