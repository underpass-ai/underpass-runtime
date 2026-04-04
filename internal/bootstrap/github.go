package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// GitHubBundle returns GitHub API tools: create_pr, check_pr_status, merge_pr.
func GitHubBundle() Bundle {
	return Bundle{
		Name: "github",
		Build: func(cfg Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewGitHubCreatePRHandler(cfg.CommandRunner),
				tooladapter.NewGitHubCheckPRStatusHandler(cfg.CommandRunner),
				tooladapter.NewGitHubMergePRHandler(cfg.CommandRunner),
			}
		},
	}
}
