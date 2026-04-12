package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	treeDefaultDepth  = 3
	treeMaxDepth      = 10
	treeMaxEntries    = 2000
	treeKeyStdout     = "stdout"
	treeDefaultIgnore = ".git,node_modules,.venv,__pycache__,vendor,.cache,dist,build,.tox,.mypy_cache"
)

// RepoTreeHandler returns a directory tree structure of the workspace.
// Provides instant codebase orientation for agents.
type RepoTreeHandler struct {
	runner app.CommandRunner
}

func NewRepoTreeHandler(runner app.CommandRunner) *RepoTreeHandler {
	return &RepoTreeHandler{runner: runner}
}

func (h *RepoTreeHandler) Name() string {
	return "repo.tree"
}

func (h *RepoTreeHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path      string `json:"path"`
		MaxDepth  int    `json:"max_depth"`
		ShowFiles bool   `json:"show_files"`
		Ignore    string `json:"ignore"`
	}{
		Path:      ".",
		MaxDepth:  treeDefaultDepth,
		ShowFiles: true,
		Ignore:    treeDefaultIgnore,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid repo.tree args", Retryable: false}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	depth := request.MaxDepth
	if depth <= 0 {
		depth = treeDefaultDepth
	}
	if depth > treeMaxDepth {
		depth = treeMaxDepth
	}

	ignoreSet := buildIgnoreSet(request.Ignore)

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, resolved, depth, request.ShowFiles, ignoreSet)
	}
	return h.invokeLocal(session.WorkspacePath, resolved, depth, request.ShowFiles, ignoreSet)
}

func (h *RepoTreeHandler) invokeLocal(workspacePath, resolved string, maxDepth int, showFiles bool, ignoreSet map[string]struct{}) (app.ToolRunResult, *domain.Error) {
	var lines []string
	entryCount := 0
	truncated := false

	err := buildTree(&lines, &entryCount, &truncated, resolved, workspacePath, "", 0, maxDepth, showFiles, ignoreSet)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	tree := strings.Join(lines, "\n")
	return app.ToolRunResult{
		Output: map[string]any{
			"tree":      tree,
			"entries":   entryCount,
			"truncated": truncated,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: treeKeyStdout, Message: fmt.Sprintf("tree: %d entries", entryCount)}},
	}, nil
}

func buildTree(lines *[]string, count *int, truncated *bool, dir, workspacePath, prefix string, depth, maxDepth int, showFiles bool, ignoreSet map[string]struct{}) error {
	if depth >= maxDepth || *truncated {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Sort: directories first, then files.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	for i, entry := range entries {
		if *count >= treeMaxEntries {
			*truncated = true
			return nil
		}

		name := entry.Name()
		if _, ignored := ignoreSet[name]; ignored {
			continue
		}

		isLast := i == len(entries)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		if entry.IsDir() {
			*lines = append(*lines, prefix+connector+name+"/")
			*count++
			buildTree(lines, count, truncated, filepath.Join(dir, name), workspacePath, childPrefix, depth+1, maxDepth, showFiles, ignoreSet)
		} else if showFiles {
			*lines = append(*lines, prefix+connector+name)
			*count++
		}
	}
	return nil
}

func (h *RepoTreeHandler) invokeRemote(
	ctx context.Context, session domain.Session,
	resolved string, maxDepth int, showFiles bool, ignoreSet map[string]struct{},
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	// Use find with maxdepth on remote.
	typeFlag := ""
	if !showFiles {
		typeFlag = "-type d"
	}

	excludes := make([]string, 0, len(ignoreSet))
	for name := range ignoreSet {
		excludes = append(excludes, fmt.Sprintf("-name %s -prune -o", shellQuote(name)))
	}
	excludeStr := strings.Join(excludes, " ")

	script := fmt.Sprintf("cd %s && find . -maxdepth %d %s %s -print | sort | head -n %d",
		shellQuote(resolved), maxDepth, excludeStr, typeFlag, treeMaxEntries+1)

	commandResult, cmdErr := runShellCommand(ctx, runner, session, script, nil, 512*1024)
	if cmdErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(cmdErr, commandResult.Output)
	}

	rawLines := splitOutputLines(commandResult.Output)
	truncated := len(rawLines) > treeMaxEntries

	return app.ToolRunResult{
		Output: map[string]any{
			"tree":      strings.Join(rawLines, "\n"),
			"entries":   len(rawLines),
			"truncated": truncated,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: treeKeyStdout, Message: fmt.Sprintf("tree: %d entries", len(rawLines))}},
	}, nil
}

func buildIgnoreSet(ignore string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, name := range strings.Split(ignore, ",") {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	return set
}
