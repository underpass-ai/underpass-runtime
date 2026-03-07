package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type SecurityScanSecretsHandler struct {
	runner app.CommandRunner
}

func NewSecurityScanSecretsHandler(runner app.CommandRunner) *SecurityScanSecretsHandler {
	return &SecurityScanSecretsHandler{runner: runner}
}

func (h *SecurityScanSecretsHandler) Name() string {
	return "security.scan_secrets"
}

func (h *SecurityScanSecretsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}{Path: ".", MaxResults: 200}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid security.scan_secrets args",
			Retryable: false,
		}
	}
	if request.MaxResults <= 0 {
		request.MaxResults = 200
	}
	if request.MaxResults > 2000 {
		request.MaxResults = 2000
	}

	scanPath, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if scanPath == "" {
		scanPath = "."
	}

	rgArgs := []string{
		"-n",
		"--hidden",
		sweRgGlobFlag, "!.git",
		sweRgGlobFlag, "!node_modules",
		sweRgGlobFlag, "!target",
		sweRgGlobFlag, "!.workspace-venv",
		"-m", strconv.Itoa(request.MaxResults),
		"-e", "AKIA[0-9A-Z]{16}",
		"-e", "BEGIN RSA PRIVATE KEY",
		"-e", "BEGIN OPENSSH PRIVATE KEY",
		"-e", `(?i)(api[_-]?key|secret|token)[[:space:]]*[:=][[:space:]]*["'][^"']{12,}["']`,
		scanPath,
	}

	runner := ensureRunner(h.runner)
	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "rg",
		Args:     rgArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	if isMissingBinaryError(runErr, commandResult, "rg") {
		grepArgs := []string{
			"-RInE",
			"--binary-files=without-match",
			"--exclude-dir=.git",
			"--exclude-dir=node_modules",
			"--exclude-dir=target",
			"--exclude-dir=.workspace-venv",
			"-m", strconv.Itoa(request.MaxResults),
			"-e", "AKIA[0-9A-Z]{16}",
			"-e", "BEGIN RSA PRIVATE KEY",
			"-e", "BEGIN OPENSSH PRIVATE KEY",
			"-e", `([Aa][Pp][Ii][_-]?[Kk][Ee][Yy]|[Ss][Ee][Cc][Rr][Ee][Tt]|[Tt][Oo][Kk][Ee][Nn])[[:space:]]*[:=][[:space:]]*["'][^"']{12,}["']`,
			scanPath,
		}
		commandResult, runErr = runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "grep",
			Args:     grepArgs,
			MaxBytes: 2 * 1024 * 1024,
		})
	}

	// rg/grep exit 1 when there are no matches; that's a successful "clean" scan.
	if runErr != nil && commandResult.ExitCode != 1 {
		result := app.ToolRunResult{
			ExitCode: commandResult.ExitCode,
			Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: commandResult.Output}},
			Output: map[string]any{
				"findings_count": 0,
				"findings":       []any{},
				"truncated":      false,
				"output":         commandResult.Output,
			},
			Artifacts: []app.ArtifactPayload{{
				Name:        sweArtifactSecretsScanOutput,
				ContentType: sweTextPlain,
				Data:        []byte(commandResult.Output),
			}},
		}
		return result, toToolError(runErr, commandResult.Output)
	}

	findings := parseSecretFindings(commandResult.Output, request.MaxResults)
	result := app.ToolRunResult{
		ExitCode: 0,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: commandResult.Output}},
		Output: map[string]any{
			"findings_count": len(findings),
			"findings":       findings,
			"truncated":      len(findings) >= request.MaxResults,
			"output":         commandResult.Output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweArtifactSecretsScanOutput,
			ContentType: sweTextPlain,
			Data:        []byte(commandResult.Output),
		}},
	}
	return result, nil
}

func parseSecretFindings(output string, maxResults int) []map[string]any {
	lines := splitOutputLines(output)
	findings := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		lineNo, _ := strconv.Atoi(parts[1])
		snippet := strings.TrimSpace(parts[2])
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		findings = append(findings, map[string]any{
			"path":    strings.TrimSpace(parts[0]),
			"line":    lineNo,
			"snippet": snippet,
		})
		if len(findings) >= maxResults {
			break
		}
	}
	return findings
}
