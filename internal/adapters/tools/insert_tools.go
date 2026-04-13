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
	insertKeyStdout = "stdout"
)

// FSInsertHandler inserts text at a specific line number without replacing
// existing content. Complements fs.edit for adding new code.
type FSInsertHandler struct {
	runner app.CommandRunner
}

func NewFSInsertHandler(runner app.CommandRunner) *FSInsertHandler {
	return &FSInsertHandler{runner: runner}
}

func (h *FSInsertHandler) Name() string {
	return "fs.insert"
}

func (h *FSInsertHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path    string `json:"path"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.insert args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}
	if request.Content == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "content is required", Retryable: false}
	}
	if request.Line < 0 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "line must be >= 0 (0 = prepend)", Retryable: false}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		SaveUndoSnapshotRemote(ctx, h.runner, session, resolved)
	} else {
		SaveUndoSnapshot(session.WorkspacePath, resolved)
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved, request.Line, request.Content)
	}
	return h.invokeLocal(request.Path, resolved, request.Line, request.Content)
}

func (h *FSInsertHandler) invokeLocal(path, resolved string, line int, content string) (app.ToolRunResult, *domain.Error) {
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathNotExist, Retryable: false}
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	result, insertErr := fsInsertAtLine(string(data), line, content)
	if insertErr != nil {
		return app.ToolRunResult{}, insertErr
	}

	if err := os.WriteFile(resolved, []byte(result), 0o644); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	return fsInsertResult(path, result, line), nil
}

func (h *FSInsertHandler) invokeRemote(
	ctx context.Context, session domain.Session,
	path, resolved string, line int, content string,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	readResult, readErr := runShellCommand(ctx, runner, session, fmt.Sprintf("cat %s", shellQuote(resolved)), nil, 1024*1024)
	if readErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(readErr, readResult.Output)
	}

	result, insertErr := fsInsertAtLine(readResult.Output, line, content)
	if insertErr != nil {
		return app.ToolRunResult{}, insertErr
	}

	writeResult, writeErr := runShellCommand(ctx, runner, session, fmt.Sprintf("cat > %s", shellQuote(resolved)), []byte(result), 256*1024)
	if writeErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(writeErr, writeResult.Output)
	}

	return fsInsertResult(path, result, line), nil
}

// fsInsertAtLine inserts content after the given line number.
// Line 0 means prepend (insert before line 1).
func fsInsertAtLine(original string, line int, content string) (string, *domain.Error) {
	lines := strings.Split(original, "\n")

	// Ensure content ends with newline for clean insertion.
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	insertLines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	insertAt := line
	if insertAt > len(lines) {
		insertAt = len(lines)
	}

	// Build new content: lines[:insertAt] + insertLines + lines[insertAt:]
	result := make([]string, 0, len(lines)+len(insertLines))
	result = append(result, lines[:insertAt]...)
	result = append(result, insertLines...)
	result = append(result, lines[insertAt:]...)

	return strings.Join(result, "\n"), nil
}

func fsInsertResult(path, content string, line int) app.ToolRunResult {
	hash := sha256.Sum256([]byte(content))
	totalLines := strings.Count(content, "\n") + 1
	return app.ToolRunResult{
		Output: map[string]any{
			"path":        filepath.Clean(path),
			"inserted_at": line,
			"total_lines": totalLines,
			"sha256":      hex.EncodeToString(hash[:]),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: insertKeyStdout, Message: fmt.Sprintf("inserted at line %d", line)}},
	}
}
