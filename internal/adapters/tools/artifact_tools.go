package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type ArtifactUploadHandler struct {
	runner app.CommandRunner
}

type ArtifactDownloadHandler struct {
	runner app.CommandRunner
}

type ArtifactListHandler struct {
	runner app.CommandRunner
}

type artifactListEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

func NewArtifactUploadHandler(runner app.CommandRunner) *ArtifactUploadHandler {
	return &ArtifactUploadHandler{runner: runner}
}

func NewArtifactDownloadHandler(runner app.CommandRunner) *ArtifactDownloadHandler {
	return &ArtifactDownloadHandler{runner: runner}
}

func NewArtifactListHandler(runner app.CommandRunner) *ArtifactListHandler {
	return &ArtifactListHandler{runner: runner}
}

func (h *ArtifactUploadHandler) Name() string {
	return "artifact.upload"
}

func (h *ArtifactDownloadHandler) Name() string {
	return "artifact.download"
}

func (h *ArtifactListHandler) Name() string {
	return "artifact.list"
}

func (h *ArtifactUploadHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		MaxBytes    int    `json:"max_bytes"`
	}{
		MaxBytes: 2 * 1024 * 1024,
	}
	if err := decodeArtifactArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}

	relativePath := strings.TrimSpace(request.Path)
	if relativePath == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "path is required",
			Retryable: false,
		}
	}

	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = filepath.Base(relativePath)
	}
	contentType := strings.TrimSpace(request.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	maxBytes := clampInt(request.MaxBytes, 1024, 10*1024*1024, 2*1024*1024)

	content, truncated, readErr := readWorkspaceFile(ctx, h.runner, session, relativePath, maxBytes)
	if readErr != nil {
		return app.ToolRunResult{}, readErr
	}
	checksum := artifactSHA256Hex(content)

	summary := fmt.Sprintf("uploaded artifact %s from %s (%d bytes)", name, relativePath, len(content))
	output := map[string]any{
		"path":          relativePath,
		"artifact_name": name,
		"content_type":  contentType,
		"size_bytes":    len(content),
		"sha256":        checksum,
		"truncated":     truncated,
		"summary":       summary,
		"output":        summary,
		"exit_code":     0,
	}

	return app.ToolRunResult{
		ExitCode: 0,
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: summary,
		}},
		Output: output,
		Artifacts: []app.ArtifactPayload{
			{
				Name:        name,
				ContentType: contentType,
				Data:        content,
			},
		},
	}, nil
}

func (h *ArtifactDownloadHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		MaxBytes int    `json:"max_bytes"`
	}{
		Encoding: "base64",
		MaxBytes: 256 * 1024,
	}
	if err := decodeArtifactArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}

	relativePath := strings.TrimSpace(request.Path)
	if relativePath == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "path is required",
			Retryable: false,
		}
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Encoding))
	if encoding == "" {
		encoding = "base64"
	}
	if encoding != "base64" && encoding != "utf8" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "encoding must be one of: base64, utf8",
			Retryable: false,
		}
	}
	maxBytes := clampInt(request.MaxBytes, 1024, 10*1024*1024, 256*1024)

	content, truncated, readErr := readWorkspaceFile(ctx, h.runner, session, relativePath, maxBytes)
	if readErr != nil {
		return app.ToolRunResult{}, readErr
	}

	output := map[string]any{
		"path":       relativePath,
		"size_bytes": len(content),
		"sha256":     artifactSHA256Hex(content),
		"truncated":  truncated,
		"exit_code":  0,
	}
	if encoding == "utf8" {
		if !utf8.Valid(content) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "artifact content is not valid utf-8",
				Retryable: false,
			}
		}
		output["encoding"] = "utf8"
		output["content"] = string(content)
	} else {
		output["encoding"] = "base64"
		output["content_base64"] = base64.StdEncoding.EncodeToString(content)
	}

	summary := fmt.Sprintf("downloaded artifact from %s (%d bytes)", relativePath, len(content))
	output["summary"] = summary
	output["output"] = summary

	return app.ToolRunResult{
		ExitCode: 0,
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: summary,
		}},
		Output: output,
	}, nil
}

func (h *ArtifactListHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path       string `json:"path"`
		Recursive  bool   `json:"recursive"`
		Pattern    string `json:"pattern"`
		MaxEntries int    `json:"max_entries"`
	}{
		Path:       ".",
		Recursive:  true,
		MaxEntries: 200,
	}
	if err := decodeArtifactArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	maxEntries := clampInt(request.MaxEntries, 1, 2000, 200)

	var (
		entries []artifactListEntry
		err     *domain.Error
	)
	if isKubernetesRuntime(session) {
		entries, err = h.listRemote(ctx, session, request.Path, request.Recursive, request.Pattern, maxEntries)
	} else {
		entries, err = h.listLocal(session, request.Path, request.Recursive, request.Pattern, maxEntries)
	}
	if err != nil {
		return app.ToolRunResult{}, err
	}

	summary := fmt.Sprintf("listed %d artifact candidates", len(entries))
	return app.ToolRunResult{
		ExitCode: 0,
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: summary,
		}},
		Output: map[string]any{
			"path":      strings.TrimSpace(request.Path),
			"recursive": request.Recursive,
			"pattern":   strings.TrimSpace(request.Pattern),
			"count":     len(entries),
			"artifacts": entries,
			"summary":   summary,
			"output":    summary,
			"exit_code": 0,
			"truncated": len(entries) >= maxEntries,
		},
	}, nil
}

func (h *ArtifactListHandler) listLocal(
	session domain.Session,
	requestPath string,
	recursive bool,
	pattern string,
	maxEntries int,
) ([]artifactListEntry, *domain.Error) {
	resolved, pathErr := resolvePath(session, requestPath)
	if pathErr != nil {
		return nil, pathErr
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   err.Error(),
			Retryable: false,
		}
	}

	return collectLocalArtifactEntries(session.WorkspacePath, resolved, info, recursive, pattern, maxEntries)
}

func collectLocalArtifactEntries(workspacePath, resolved string, info os.FileInfo, recursive bool, pattern string, maxEntries int) ([]artifactListEntry, *domain.Error) {
	entries := make([]artifactListEntry, 0, maxEntries)

	if !info.IsDir() {
		appendArtifactEntry(&entries, workspacePath, resolved, info.Size(), pattern, maxEntries)
		return entries, nil
	}

	if !recursive {
		return collectFlatArtifactEntries(workspacePath, resolved, pattern, maxEntries)
	}

	collectRecursiveArtifactEntries(&entries, workspacePath, resolved, pattern, maxEntries)
	return entries, nil
}

// appendArtifactEntry resolves a path relative to workspacePath, filters it
// against pattern, and appends it to *entries when there is still capacity.
func appendArtifactEntry(entries *[]artifactListEntry, workspacePath, path string, size int64, pattern string, maxEntries int) {
	if len(*entries) >= maxEntries {
		return
	}
	rel, relErr := filepath.Rel(workspacePath, path)
	if relErr != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	if !artifactPathMatches(rel, pattern) {
		return
	}
	*entries = append(*entries, artifactListEntry{Path: rel, SizeBytes: size})
}

// collectFlatArtifactEntries reads one level of a directory and returns
// matching file entries (no subdirectory recursion).
func collectFlatArtifactEntries(workspacePath, resolved, pattern string, maxEntries int) ([]artifactListEntry, *domain.Error) {
	directoryEntries, readErr := os.ReadDir(resolved)
	if readErr != nil {
		return nil, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: readErr.Error(), Retryable: false}
	}
	entries := make([]artifactListEntry, 0, maxEntries)
	for _, entry := range directoryEntries {
		if len(entries) >= maxEntries {
			break
		}
		if entry.IsDir() {
			continue
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		appendArtifactEntry(&entries, workspacePath, filepath.Join(resolved, entry.Name()), entryInfo.Size(), pattern, maxEntries)
	}
	return entries, nil
}

// collectRecursiveArtifactEntries walks the directory tree under resolved and
// appends matching file entries to *entries.
func collectRecursiveArtifactEntries(entries *[]artifactListEntry, workspacePath, resolved, pattern string, maxEntries int) {
	_ = filepath.Walk(resolved, func(path string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if len(*entries) >= maxEntries {
			return filepath.SkipDir
		}
		if walkInfo == nil || walkInfo.IsDir() {
			return nil
		}
		appendArtifactEntry(entries, workspacePath, path, walkInfo.Size(), pattern, maxEntries)
		return nil
	})
}

func (h *ArtifactListHandler) listRemote(
	ctx context.Context,
	session domain.Session,
	requestPath string,
	recursive bool,
	pattern string,
	maxEntries int,
) ([]artifactListEntry, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return nil, runErr
	}
	resolved, pathErr := resolvePath(session, requestPath)
	if pathErr != nil {
		return nil, pathErr
	}

	maxDepth := ""
	if !recursive {
		maxDepth = "-maxdepth 1"
	}
	script := fmt.Sprintf(
		"if [ -d %s ]; then find %s %s -type f -print; "+
			"elif [ -f %s ]; then printf '%%s\\n' %s; "+
			"else echo 'path not found' >&2; exit 1; fi",
		shellQuote(resolved),
		shellQuote(resolved), maxDepth,
		shellQuote(resolved), shellQuote(resolved),
	)

	commandResult, err := runShellCommand(ctx, runner, session, script, nil, 2*1024*1024)
	if err != nil {
		return nil, toFSRunnerError(err, commandResult.Output)
	}

	lines := splitOutputLines(commandResult.Output)
	entries := make([]artifactListEntry, 0, artifactMinInt(len(lines), maxEntries))
	for _, line := range lines {
		if len(entries) >= maxEntries {
			break
		}
		rel, relErr := filepath.Rel(session.WorkspacePath, strings.TrimSpace(line))
		if relErr != nil {
			rel = strings.TrimSpace(line)
		}
		rel = filepath.ToSlash(rel)
		if !artifactPathMatches(rel, pattern) {
			continue
		}
		entries = append(entries, artifactListEntry{
			Path:      rel,
			SizeBytes: 0,
		})
	}
	return entries, nil
}

func decodeArtifactArgs(args json.RawMessage, destination any) *domain.Error {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, destination); err != nil {
		return &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid artifact tool args",
			Retryable: false,
		}
	}
	return nil
}

func readWorkspaceFile(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	relativePath string,
	maxBytes int,
) ([]byte, bool, *domain.Error) {
	maxBytes = clampInt(maxBytes, 1024, 10*1024*1024, 256*1024)
	resolved, pathErr := resolvePath(session, relativePath)
	if pathErr != nil {
		return nil, false, pathErr
	}

	if !isKubernetesRuntime(session) {
		content, err := os.ReadFile(resolved)
		if err != nil {
			return nil, false, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   err.Error(),
				Retryable: false,
			}
		}
		truncated := false
		if len(content) > maxBytes {
			content = content[:maxBytes]
			truncated = true
		}
		return content, truncated, nil
	}

	k8sRunner, runErr := resolveKubernetesRunner(runner)
	if runErr != nil {
		return nil, false, runErr
	}

	script := fmt.Sprintf(
		"if [ ! -f %s ]; then echo 'path is not a regular file' >&2; exit 1; fi; "+
			"dd if=%s bs=1 count=%d 2>/dev/null | base64 | tr -d '\\n'",
		shellQuote(resolved),
		shellQuote(resolved),
		maxBytes+1,
	)
	commandResult, err := runShellCommand(ctx, k8sRunner, session, script, nil, (maxBytes*2)+4096)
	if err != nil {
		return nil, false, toFSRunnerError(err, commandResult.Output)
	}

	encoded := strings.TrimSpace(commandResult.Output)
	content, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if decodeErr != nil {
		return nil, false, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "failed to decode artifact content",
			Retryable: false,
		}
	}
	truncated := false
	if len(content) > maxBytes {
		content = content[:maxBytes]
		truncated = true
	}
	return content, truncated, nil
}

func artifactSHA256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func artifactPathMatches(path, pattern string) bool {
	matcher := strings.TrimSpace(pattern)
	if matcher == "" {
		return true
	}
	matched, err := filepath.Match(matcher, path)
	if err == nil && matched {
		return true
	}
	base := filepath.Base(path)
	matched, err = filepath.Match(matcher, base)
	return err == nil && matched
}

func artifactMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
