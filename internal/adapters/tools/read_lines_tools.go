package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	readLinesMaxRange  = 500
	readLinesKeyStdout = "stdout"
)

// FSReadLinesHandler reads a specific line range from a file. Essential for
// small models that cannot process large files in one pass.
type FSReadLinesHandler struct {
	runner app.CommandRunner
}

func NewFSReadLinesHandler(runner app.CommandRunner) *FSReadLinesHandler {
	return &FSReadLinesHandler{runner: runner}
}

func (h *FSReadLinesHandler) Name() string {
	return "fs.read_lines"
}

func (h *FSReadLinesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}{
		StartLine: 1,
		EndLine:   100,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.read_lines args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}
	if request.StartLine < 1 {
		request.StartLine = 1
	}
	if request.EndLine < request.StartLine {
		request.EndLine = request.StartLine
	}
	if request.EndLine-request.StartLine+1 > readLinesMaxRange {
		request.EndLine = request.StartLine + readLinesMaxRange - 1
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved, request.StartLine, request.EndLine)
	}
	return h.invokeLocal(request.Path, resolved, request.StartLine, request.EndLine)
}

func (h *FSReadLinesHandler) invokeLocal(path, resolved string, startLine, endLine int) (app.ToolRunResult, *domain.Error) {
	f, err := os.Open(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathNotExist, Retryable: false}
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	defer f.Close()

	var lines []string
	totalLines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		totalLines++
		if totalLines >= startLine && totalLines <= endLine {
			lines = append(lines, fmt.Sprintf("%d\t%s", totalLines, scanner.Text()))
		}
		if totalLines > endLine {
			// Keep counting total lines.
		}
	}
	// Count remaining lines without storing.
	for scanner.Scan() {
		totalLines++
	}

	content := strings.Join(lines, "\n")
	return app.ToolRunResult{
		Output: map[string]any{
			"path":        path,
			"start_line":  startLine,
			"end_line":    endLine,
			"total_lines": totalLines,
			"content":     content,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: readLinesKeyStdout, Message: fmt.Sprintf("read lines %d-%d of %d", startLine, endLine, totalLines)}},
	}, nil
}

func (h *FSReadLinesHandler) invokeRemote(
	ctx context.Context, session domain.Session,
	path, resolved string, startLine, endLine int,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	// Use sed to extract lines with line numbers, and wc -l for total.
	script := fmt.Sprintf(
		"sed -n '%d,%dp' %s | nl -ba -nln -v%d; echo '---TOTAL---'; wc -l < %s",
		startLine, endLine, shellQuote(resolved), startLine, shellQuote(resolved),
	)
	commandResult, cmdErr := runShellCommand(ctx, runner, session, script, nil, 512*1024)
	if cmdErr != nil {
		return app.ToolRunResult{}, toFSRunnerError(cmdErr, commandResult.Output)
	}

	parts := strings.SplitN(commandResult.Output, "---TOTAL---", 2)
	content := strings.TrimSpace(parts[0])
	totalLines := 0
	if len(parts) > 1 {
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &totalLines)
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"path":        path,
			"start_line":  startLine,
			"end_line":    endLine,
			"total_lines": totalLines,
			"content":     content,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: readLinesKeyStdout, Message: fmt.Sprintf("read lines %d-%d of %d", startLine, endLine, totalLines)}},
	}, nil
}
