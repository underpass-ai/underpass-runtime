package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testFileDefaultTimeout = 120
	testFileMaxTimeout     = 600
	testFileKeyStdout      = "stdout"
)

// RepoTestFileHandler runs tests for a specific file or package.
// More targeted than repo.test which runs the entire suite.
type RepoTestFileHandler struct {
	runner app.CommandRunner
}

func NewRepoTestFileHandler(runner app.CommandRunner) *RepoTestFileHandler {
	return &RepoTestFileHandler{runner: runner}
}

func (h *RepoTestFileHandler) Name() string { return "repo.test_file" }

func (h *RepoTestFileHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path           string `json:"path"`
		Pattern        string `json:"pattern"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}{
		TimeoutSeconds: testFileDefaultTimeout,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid repo.test_file args", Retryable: false}
	}
	if strings.TrimSpace(request.Path) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "path is required", Retryable: false}
	}

	timeout := request.TimeoutSeconds
	if timeout <= 0 {
		timeout = testFileDefaultTimeout
	}
	if timeout > testFileMaxTimeout {
		timeout = testFileMaxTimeout
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	// Build a test command based on file extension.
	path := strings.TrimSpace(request.Path)
	var command string
	switch {
	case strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".go"):
		pkg := "./" + strings.TrimSuffix(path, "/"+lastSegment(path))
		if request.Pattern != "" {
			command = fmt.Sprintf("go test -v -run %s -timeout %ds %s", shellQuote(request.Pattern), timeout, shellQuote(pkg))
		} else {
			command = fmt.Sprintf("go test -v -timeout %ds %s", timeout, shellQuote(pkg))
		}
	case strings.HasSuffix(path, "_test.py") || strings.HasSuffix(path, ".py"):
		if request.Pattern != "" {
			command = fmt.Sprintf("python -m pytest -v -k %s %s --timeout=%d", shellQuote(request.Pattern), shellQuote(path), timeout)
		} else {
			command = fmt.Sprintf("python -m pytest -v %s --timeout=%d", shellQuote(path), timeout)
		}
	case strings.HasSuffix(path, ".test.js") || strings.HasSuffix(path, ".test.ts") ||
		strings.HasSuffix(path, ".spec.js") || strings.HasSuffix(path, ".spec.ts"):
		command = fmt.Sprintf("npx jest %s --no-coverage", shellQuote(path))
	case strings.HasSuffix(path, "_test.rs") || strings.HasSuffix(path, ".rs"):
		if request.Pattern != "" {
			command = fmt.Sprintf("cargo test %s -- --nocapture", shellQuote(request.Pattern))
		} else {
			command = "cargo test -- --nocapture"
		}
	default:
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeInvalidArgument, Message: "unsupported test file type", Retryable: false,
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd: session.WorkspacePath, Command: "sh", Args: []string{"-lc", command}, MaxBytes: 512 * 1024,
	})

	passed := runErr == nil && commandResult.ExitCode == 0
	output := map[string]any{
		"path":      path,
		"command":   command,
		"passed":    passed,
		"exit_code": commandResult.ExitCode,
		"output":    commandResult.Output,
	}

	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Output:   output,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: testFileKeyStdout, Message: commandResult.Output}},
	}
	// Non-zero exit means test failure, not infrastructure error.
	return result, nil
}

func lastSegment(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}
