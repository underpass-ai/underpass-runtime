package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	undoDir       = ".underpass-undo"
	undoKeyStdout = "stdout"
)

// WorkspaceUndoEditHandler reverts the last edit to a specific file.
// Works by reading from a snapshot directory maintained by edit/write/insert
// operations.
type WorkspaceUndoEditHandler struct {
	runner app.CommandRunner
}

func NewWorkspaceUndoEditHandler(runner app.CommandRunner) *WorkspaceUndoEditHandler {
	return &WorkspaceUndoEditHandler{runner: runner}
}

func (h *WorkspaceUndoEditHandler) Name() string {
	return "workspace.undo_edit"
}

func (h *WorkspaceUndoEditHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path string `json:"path"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid workspace.undo_edit args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved)
	}
	return h.invokeLocal(request.Path, resolved, session.WorkspacePath)
}

func (h *WorkspaceUndoEditHandler) invokeLocal(path, resolved, workspacePath string) (app.ToolRunResult, *domain.Error) {
	snapshotPath := undoSnapshotPath(workspacePath, path)
	snapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "no undo snapshot for this file", Retryable: false}
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	if err := os.WriteFile(resolved, snapshot, 0o644); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	// Remove snapshot after successful undo — only one level of undo.
	os.Remove(snapshotPath)

	hash := sha256.Sum256(snapshot)
	return app.ToolRunResult{
		Output: map[string]any{
			"path":     filepath.Clean(path),
			"restored": true,
			"sha256":   hex.EncodeToString(hash[:]),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: undoKeyStdout, Message: fmt.Sprintf("restored %s from undo snapshot", path)}},
	}, nil
}

func (h *WorkspaceUndoEditHandler) invokeRemote(
	ctx context.Context, session domain.Session,
	path, resolved string,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	snapshotPath := undoSnapshotPath(session.WorkspacePath, path)
	checkScript := fmt.Sprintf("cat %s", shellQuote(snapshotPath))
	readResult, readErr := runShellCommand(ctx, runner, session, checkScript, nil, 1024*1024)
	if readErr != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "no undo snapshot for this file", Retryable: false}
	}

	writeScript := fmt.Sprintf("cat > %s && rm -f %s", shellQuote(resolved), shellQuote(snapshotPath))
	writeResult, writeErr := runShellCommand(ctx, runner, session, writeScript, []byte(readResult.Output), 256*1024)
	if writeErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(writeErr, writeResult.Output)
	}

	hash := sha256.Sum256([]byte(readResult.Output))
	return app.ToolRunResult{
		Output: map[string]any{
			"path":     filepath.Clean(path),
			"restored": true,
			"sha256":   hex.EncodeToString(hash[:]),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: undoKeyStdout, Message: fmt.Sprintf("restored %s from undo snapshot", path)}},
	}, nil
}

// SaveUndoSnapshot saves the current file content before modification.
// Called by fs.edit, fs.write_file, and fs.insert before mutating files.
func SaveUndoSnapshot(workspacePath, resolved string) {
	content, err := os.ReadFile(resolved)
	if err != nil {
		return // File doesn't exist yet — nothing to snapshot.
	}

	rel, err := filepath.Rel(workspacePath, resolved)
	if err != nil {
		return
	}

	snapshotFile := undoSnapshotPath(workspacePath, rel)
	os.MkdirAll(filepath.Dir(snapshotFile), 0o755)
	os.WriteFile(snapshotFile, content, 0o644)
}

func undoSnapshotPath(workspacePath, relativePath string) string {
	safe := strings.ReplaceAll(filepath.Clean(relativePath), string(filepath.Separator), "__")
	return filepath.Join(workspacePath, undoDir, safe)
}
