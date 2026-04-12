package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	globMaxResults    = 5000
	globDefaultLimit  = 1000
	globKeyStdout     = "stdout"
	globErrPatternReq = "pattern is required"
)

// FSGlobHandler finds files by glob pattern within the workspace.
// Supports doublestar patterns (e.g. **/*.go, src/**/*.ts).
type FSGlobHandler struct {
	runner app.CommandRunner
}

func NewFSGlobHandler(runner app.CommandRunner) *FSGlobHandler {
	return &FSGlobHandler{runner: runner}
}

func (h *FSGlobHandler) Name() string {
	return "fs.glob"
}

func (h *FSGlobHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Pattern  string `json:"pattern"`
		Path     string `json:"path"`
		MaxFiles int    `json:"max_files"`
	}{
		Path:     ".",
		MaxFiles: globDefaultLimit,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.glob args", Retryable: false,
		}
	}

	pattern := strings.TrimSpace(request.Pattern)
	if pattern == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: globErrPatternReq, Retryable: false,
		}
	}

	if !doublestar.ValidatePattern(pattern) {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: "invalid glob pattern", Retryable: false,
		}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	limit := request.MaxFiles
	if limit <= 0 {
		limit = globDefaultLimit
	}
	if limit > globMaxResults {
		limit = globMaxResults
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, resolved, pattern, limit)
	}
	return h.invokeLocal(session.WorkspacePath, resolved, pattern, limit)
}

func (h *FSGlobHandler) invokeLocal(workspacePath, resolved, pattern string, limit int) (app.ToolRunResult, *domain.Error) {
	matches := make([]string, 0, limit)
	truncated := false

	root := os.DirFS(resolved)
	err := doublestar.GlobWalk(root, pattern, func(path string, d fs.DirEntry) error {
		if len(matches) >= limit {
			truncated = true
			return doublestar.SkipDir
		}
		// Return workspace-relative path.
		relPath, relErr := filepath.Rel(workspacePath, filepath.Join(resolved, path))
		if relErr != nil {
			relPath = path
		}
		matches = append(matches, relPath)
		return nil
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("glob walk failed: %v", err), Retryable: false,
		}
	}

	return globResult(matches, truncated), nil
}

func (h *FSGlobHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	resolved, pattern string,
	limit int,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	// Use find + shell glob matching on remote containers.
	script := fmt.Sprintf("cd %s && find . -type f 2>/dev/null | head -n %d", shellQuote(resolved), limit+1)
	commandResult, cmdErr := runShellCommand(ctx, runner, session, script, nil, 512*1024)
	if cmdErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(cmdErr, commandResult.Output)
	}

	allFiles := splitOutputLines(commandResult.Output)
	matches := make([]string, 0, len(allFiles))
	for _, file := range allFiles {
		clean := strings.TrimPrefix(file, "./")
		if clean == "" {
			continue
		}
		matched, matchErr := doublestar.Match(pattern, clean)
		if matchErr != nil {
			continue
		}
		if matched {
			matches = append(matches, clean)
			if len(matches) >= limit {
				break
			}
		}
	}

	truncated := len(matches) >= limit
	return globResult(matches, truncated), nil
}

func globResult(matches []string, truncated bool) app.ToolRunResult {
	return app.ToolRunResult{
		Output: map[string]any{
			"matches":   matches,
			"count":     len(matches),
			"truncated": truncated,
		},
		Logs: []domain.LogLine{
			{At: time.Now().UTC(), Channel: globKeyStdout, Message: fmt.Sprintf("found %d files", len(matches))},
		},
	}
}
