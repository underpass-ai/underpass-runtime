package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	fsKeyStdout         = "stdout"
	fsKeySourcePath     = "source_path"
	fsKeyDestPath       = "destination_path"
	fsKeyRecursive      = "recursive"
	fsErrPathRequired   = "path is required"
	fsKeySizeBytes      = "size_bytes"
	fsKeyCreateParents  = "create_parents"
	fsErrPathNotExist   = "path does not exist"
	fsKeyRejectConflict = "reject_on_conflict"
	fsKeyModifiedAt     = "modified_at"
	fsKeyMaxResults     = "max_results"
	fsKeyMaxEntries     = "max_entries"
	fsKeyMaxBytes       = "max_bytes"
	fsShellIfE          = "if [ -e "
	fsShellIfD          = "if [ -d "
	fsShellMkdirP       = "mkdir -p "
	fsShellIfNotE       = "if [ ! -e "
	fsShellThenSuffix   = " ]; then"
	fsShellThenRmRf     = " ]; then rm -rf "
)

type FSListHandler struct {
	runner app.CommandRunner
}

type FSReadHandler struct {
	runner app.CommandRunner
}

type FSWriteHandler struct {
	runner app.CommandRunner
}

type FSMkdirHandler struct {
	runner app.CommandRunner
}

type FSMoveHandler struct {
	runner app.CommandRunner
}

type FSCopyHandler struct {
	runner app.CommandRunner
}

type FSDeleteHandler struct {
	runner app.CommandRunner
}

type FSStatHandler struct {
	runner app.CommandRunner
}

type FSPatchHandler struct {
	runner app.CommandRunner
}

type FSSearchHandler struct {
	runner app.CommandRunner
}

type fsListEntry struct {
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size_bytes"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified_at"`
}

func NewFSListHandler(runner app.CommandRunner) *FSListHandler {
	return &FSListHandler{runner: runner}
}

func NewFSReadHandler(runner app.CommandRunner) *FSReadHandler {
	return &FSReadHandler{runner: runner}
}

func NewFSWriteHandler(runner app.CommandRunner) *FSWriteHandler {
	return &FSWriteHandler{runner: runner}
}

func NewFSMkdirHandler(runner app.CommandRunner) *FSMkdirHandler {
	return &FSMkdirHandler{runner: runner}
}

func NewFSMoveHandler(runner app.CommandRunner) *FSMoveHandler {
	return &FSMoveHandler{runner: runner}
}

func NewFSCopyHandler(runner app.CommandRunner) *FSCopyHandler {
	return &FSCopyHandler{runner: runner}
}

func NewFSDeleteHandler(runner app.CommandRunner) *FSDeleteHandler {
	return &FSDeleteHandler{runner: runner}
}

func NewFSStatHandler(runner app.CommandRunner) *FSStatHandler {
	return &FSStatHandler{runner: runner}
}

func NewFSPatchHandler(runner app.CommandRunner) *FSPatchHandler {
	return &FSPatchHandler{runner: runner}
}

func NewFSSearchHandler(runner app.CommandRunner) *FSSearchHandler {
	return &FSSearchHandler{runner: runner}
}

func (h *FSListHandler) Name() string {
	return "fs.list"
}

func (h *FSListHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path       string `json:"path"`
		Recursive  bool   `json:"recursive"`
		MaxEntries int    `json:"max_entries"`
	}{Path: ".", MaxEntries: 200}

	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.list args", Retryable: false}
		}
	}
	if request.MaxEntries <= 0 {
		request.MaxEntries = 200
	}
	if request.MaxEntries > 1000 {
		request.MaxEntries = 1000
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request)
	}
	return h.invokeLocal(session, request)
}

func (h *FSListHandler) invokeLocal(session domain.Session, request struct {
	Path       string `json:"path"`
	Recursive  bool   `json:"recursive"`
	MaxEntries int    `json:"max_entries"`
}) (app.ToolRunResult, *domain.Error) {
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	stat, err := os.Stat(resolved)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	entries := make([]fsListEntry, 0, request.MaxEntries)
	appendEntry := func(path string, info os.FileInfo) {
		rel, relErr := filepath.Rel(session.WorkspacePath, path)
		if relErr != nil {
			rel = info.Name()
		}
		typeName := "file"
		if info.IsDir() {
			typeName = "dir"
		}
		entries = append(entries, fsListEntry{
			Path:     rel,
			Type:     typeName,
			Size:     info.Size(),
			Mode:     info.Mode().String(),
			Modified: info.ModTime().UTC(),
		})
	}

	if !stat.IsDir() {
		appendEntry(resolved, stat)
	} else if request.Recursive {
		isFull := func() bool { return len(entries) >= request.MaxEntries }
		if walkErr := fsListWalkRecursive(resolved, isFull, appendEntry); walkErr != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: walkErr.Error(), Retryable: false}
		}
	} else {
		isFull := func() bool { return len(entries) >= request.MaxEntries }
		if readErr := fsListReadFlat(resolved, isFull, appendEntry); readErr != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: readErr.Error(), Retryable: false}
		}
	}

	return app.ToolRunResult{
		Output: map[string]any{"entries": entries, "count": len(entries)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("listed %d entries", len(entries))}},
	}, nil
}

// fsListWalkRecursive walks resolved recursively, calling appendEntry for each
// non-root path. It stops once isFull returns true.
func fsListWalkRecursive(resolved string, isFull func() bool, appendEntry func(string, os.FileInfo)) error {
	walkStop := errors.New("stop-walk")
	walkErr := filepath.Walk(resolved, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != resolved {
			appendEntry(path, info)
		}
		if isFull() {
			return walkStop
		}
		return nil
	})
	if errors.Is(walkErr, walkStop) {
		return nil
	}
	return walkErr
}

// fsListReadFlat reads one level of resolved, calling appendEntry for each
// entry until isFull returns true.
func fsListReadFlat(resolved string, isFull func() bool, appendEntry func(string, os.FileInfo)) error {
	dirEntries, readErr := os.ReadDir(resolved)
	if readErr != nil {
		return readErr
	}
	for _, current := range dirEntries {
		if isFull() {
			break
		}
		info, infoErr := current.Info()
		if infoErr != nil {
			continue
		}
		appendEntry(filepath.Join(resolved, current.Name()), info)
	}
	return nil
}

func (h *FSListHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	request struct {
		Path       string `json:"path"`
		Recursive  bool   `json:"recursive"`
		MaxEntries int    `json:"max_entries"`
	},
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	maxDepthPart := "-maxdepth 1"
	if request.Recursive {
		maxDepthPart = ""
	}

	script := fmt.Sprintf(
		"if [ -d %s ]; then "+
			"find %s -mindepth 1 %s -type d -print | sed 's#^#dir\\t#'; "+
			"find %s -mindepth 1 %s -type f -print | sed 's#^#file\\t#'; "+
			"elif [ -f %s ]; then printf 'file\\t%%s\\n' %s; "+
			"else echo 'path not found' >&2; exit 1; fi",
		shellQuote(resolved),
		shellQuote(resolved), maxDepthPart,
		shellQuote(resolved), maxDepthPart,
		shellQuote(resolved), shellQuote(resolved),
	)

	commandResult, err := runShellCommand(ctx, runner, session, script, nil, 1024*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}

	lines := splitOutputLines(commandResult.Output)
	entries := make([]fsListEntry, 0, request.MaxEntries)
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		typeName := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])
		if typeName != "dir" {
			typeName = "file"
		}
		rel, relErr := filepath.Rel(session.WorkspacePath, path)
		if relErr != nil {
			rel = path
		}
		entries = append(entries, fsListEntry{
			Path: rel,
			Type: typeName,
		})
		if len(entries) >= request.MaxEntries {
			break
		}
	}

	return app.ToolRunResult{
		Output: map[string]any{"entries": entries, "count": len(entries)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("listed %d entries", len(entries))}},
	}, nil
}

func (h *FSReadHandler) Name() string {
	return "fs.read_file"
}

func (h *FSReadHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}{MaxBytes: 64 * 1024}

	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.read_file args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}
	if request.MaxBytes <= 0 {
		request.MaxBytes = 64 * 1024
	}
	if request.MaxBytes > 1024*1024 {
		request.MaxBytes = 1024 * 1024
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request)
	}
	return h.invokeLocal(session, request)
}

func (h *FSReadHandler) invokeLocal(session domain.Session, request struct {
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes"`
}) (app.ToolRunResult, *domain.Error) {
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if len(content) > request.MaxBytes {
		content = content[:request.MaxBytes]
	}
	return fsReadResult(request.Path, content), nil
}

func (h *FSReadHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	request struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	},
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	script := fmt.Sprintf(
		"if [ ! -f %s ]; then echo 'path is not a regular file' >&2; exit 1; fi; "+
			"dd if=%s bs=1 count=%d 2>/dev/null | base64 | tr -d '\\n'",
		shellQuote(resolved),
		shellQuote(resolved),
		request.MaxBytes,
	)
	maxOutputBytes := (request.MaxBytes*2 + 4096)
	commandResult, err := runShellCommand(ctx, runner, session, script, nil, maxOutputBytes)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}

	encoded := strings.TrimSpace(commandResult.Output)
	content, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if decodeErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "failed to decode remote file payload",
			Retryable: false,
		}
	}
	return fsReadResult(request.Path, content), nil
}

func fsReadResult(path string, content []byte) app.ToolRunResult {
	encoding := "utf8"
	value := string(content)
	if !utf8.Valid(content) {
		encoding = "base64"
		value = base64.StdEncoding.EncodeToString(content)
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"path":         filepath.Clean(path),
			"encoding":     encoding,
			"content":      value,
			fsKeySizeBytes: len(content),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("read %d bytes", len(content))}},
	}
}

func (h *FSWriteHandler) Name() string {
	return "fs.write_file"
}

func (h *FSWriteHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path          string `json:"path"`
		Content       string `json:"content"`
		Encoding      string `json:"encoding"`
		CreateParents bool   `json:"create_parents"`
	}{Encoding: "utf8", CreateParents: true}

	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.write_file args", Retryable: false}
	}
	if request.Path == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}

	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	payload, payloadErr := decodeFSWritePayload(request.Content, request.Encoding)
	if payloadErr != nil {
		return app.ToolRunResult{}, payloadErr
	}
	if len(payload) > 1024*1024 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "content exceeds 1MB limit", Retryable: false}
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved, payload, request.CreateParents)
	}
	return h.invokeLocal(request.Path, resolved, payload, request.CreateParents)
}

func (h *FSWriteHandler) invokeLocal(path, resolved string, payload []byte, createParents bool) (app.ToolRunResult, *domain.Error) {
	if createParents {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
		}
	}

	if err := os.WriteFile(resolved, payload, 0o644); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	return fsWriteResult(path, payload), nil
}

func (h *FSWriteHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	path string,
	resolved string,
	payload []byte,
	createParents bool,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	var script string
	if createParents {
		script = fmt.Sprintf("mkdir -p %s && cat > %s", shellQuote(filepath.Dir(resolved)), shellQuote(resolved))
	} else {
		script = fmt.Sprintf("cat > %s", shellQuote(resolved))
	}

	commandResult, err := runShellCommand(ctx, runner, session, script, payload, 256*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}
	return fsWriteResult(path, payload), nil
}

func fsWriteResult(path string, payload []byte) app.ToolRunResult {
	hash := sha256.Sum256(payload)
	return app.ToolRunResult{
		Output: map[string]any{
			"path":          filepath.Clean(path),
			"bytes_written": len(payload),
			"sha256":        hex.EncodeToString(hash[:]),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("wrote %d bytes", len(payload))}},
	}
}

func decodeFSWritePayload(content, encoding string) ([]byte, *domain.Error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf8":
		return []byte(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid base64 content", Retryable: false}
		}
		return decoded, nil
	default:
		return nil, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "encoding must be utf8 or base64", Retryable: false}
	}
}

func (h *FSMkdirHandler) Name() string {
	return "fs.mkdir"
}

func (h *FSMkdirHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path          string `json:"path"`
		CreateParents bool   `json:"create_parents"`
		Mode          string `json:"mode"`
		ExistOk       bool   `json:"exist_ok"`
	}{
		CreateParents: true,
		Mode:          "0755",
		ExistOk:       true,
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.mkdir args", Retryable: false}
	}
	if strings.TrimSpace(request.Path) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}

	modeValue, parseErr := parseFSMkdirMode(request.Mode)
	if parseErr != nil {
		return app.ToolRunResult{}, parseErr
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved, request.CreateParents, request.ExistOk, modeValue)
	}
	return h.invokeLocal(request.Path, resolved, request.CreateParents, request.ExistOk, modeValue)
}

func (h *FSMkdirHandler) invokeLocal(path, resolved string, createParents, existOk bool, mode os.FileMode) (app.ToolRunResult, *domain.Error) {
	if info, statErr := os.Stat(resolved); statErr == nil {
		if !info.IsDir() {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "path exists and is not a directory", Retryable: false}
		}
		if !existOk {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "directory already exists", Retryable: false}
		}
		return app.ToolRunResult{
			Output: map[string]any{"path": filepath.Clean(path), "created": false, "mode": formatPermission(mode)},
			Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "directory already exists"}},
		}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: statErr.Error(), Retryable: false}
	}

	var err error
	if createParents {
		err = os.MkdirAll(resolved, mode)
	} else {
		err = os.Mkdir(resolved, mode)
	}
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	_ = os.Chmod(resolved, mode)
	return app.ToolRunResult{
		Output: map[string]any{"path": filepath.Clean(path), "created": true, "mode": formatPermission(mode)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "directory created"}},
	}, nil
}

func (h *FSMkdirHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	path string,
	resolved string,
	createParents bool,
	existOk bool,
	mode os.FileMode,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	mkdirCmd := "mkdir " + shellQuote(resolved)
	if createParents {
		mkdirCmd = fsShellMkdirP + shellQuote(resolved)
	}
	existFlag := "0"
	if existOk {
		existFlag = "1"
	}
	script := strings.Join([]string{
		fsShellIfE + shellQuote(resolved) + fsShellThenSuffix,
		"  " + fsShellIfD + shellQuote(resolved) + fsShellThenSuffix,
		"    if [ \"" + existFlag + "\" = \"1\" ]; then exit 0; fi",
		"    echo 'directory already exists' >&2; exit 1",
		"  fi",
		"  echo 'path exists and is not a directory' >&2; exit 1",
		"fi",
		mkdirCmd,
		"chmod " + formatPermission(mode) + " " + shellQuote(resolved) + " >/dev/null 2>&1 || true",
	}, "\n")

	commandResult, err := runShellCommand(ctx, runner, session, script, nil, 64*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}
	return app.ToolRunResult{
		Output: map[string]any{"path": filepath.Clean(path), "created": true, "mode": formatPermission(mode)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "directory created"}},
	}, nil
}

func (h *FSMoveHandler) Name() string {
	return "fs.move"
}

func (h *FSMoveHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		SourcePath      string `json:"source_path"`
		DestinationPath string `json:"destination_path"`
		Overwrite       bool   `json:"overwrite"`
		CreateParents   bool   `json:"create_parents"`
	}{CreateParents: true}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.move args", Retryable: false}
	}
	if strings.TrimSpace(request.SourcePath) == "" || strings.TrimSpace(request.DestinationPath) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "source_path and destination_path are required", Retryable: false}
	}

	srcResolved, srcErr := resolvePath(session, request.SourcePath)
	if srcErr != nil {
		return app.ToolRunResult{}, srcErr
	}
	dstResolved, dstErr := resolvePath(session, request.DestinationPath)
	if dstErr != nil {
		return app.ToolRunResult{}, dstErr
	}
	if srcResolved == dstResolved {
		return app.ToolRunResult{
			Output: map[string]any{
				fsKeySourcePath: filepath.Clean(request.SourcePath),
				fsKeyDestPath:   filepath.Clean(request.DestinationPath),
				"moved":          false,
			},
			Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "source and destination are identical"}},
		}, nil
	}

	mp := fsMoveParams{
		sourcePath:      request.SourcePath,
		destinationPath: request.DestinationPath,
		srcResolved:     srcResolved,
		dstResolved:     dstResolved,
		overwrite:       request.Overwrite,
		createParents:   request.CreateParents,
	}
	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, mp)
	}
	return h.invokeLocal(mp)
}

type fsMoveParams struct {
	sourcePath      string
	destinationPath string
	srcResolved     string
	dstResolved     string
	overwrite       bool
	createParents   bool
}

func (h *FSMoveHandler) invokeLocal(p fsMoveParams) (app.ToolRunResult, *domain.Error) {
	if _, err := os.Stat(p.srcResolved); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if p.createParents {
		if err := os.MkdirAll(filepath.Dir(p.dstResolved), 0o755); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
		}
	}

	if _, err := os.Stat(p.dstResolved); err == nil {
		if !p.overwrite {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "destination already exists", Retryable: false}
		}
		if err := os.RemoveAll(p.dstResolved); err != nil {
			return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	if err := os.Rename(p.srcResolved, p.dstResolved); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	return app.ToolRunResult{
		Output: map[string]any{
			fsKeySourcePath: filepath.Clean(p.sourcePath),
			fsKeyDestPath:   filepath.Clean(p.destinationPath),
			"moved":         true,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "moved path"}},
	}, nil
}

func (h *FSMoveHandler) invokeRemote(ctx context.Context, session domain.Session, p fsMoveParams) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	scriptLines := []string{
		fsShellIfNotE + shellQuote(p.srcResolved) + fsShellThenSuffix + " echo 'source path not found' >&2; exit 1; fi",
	}
	if p.createParents {
		scriptLines = append(scriptLines, fsShellMkdirP+shellQuote(filepath.Dir(p.dstResolved)))
	}
	if p.overwrite {
		scriptLines = append(scriptLines, fsShellIfE+shellQuote(p.dstResolved)+fsShellThenRmRf+shellQuote(p.dstResolved)+"; fi")
	} else {
		scriptLines = append(scriptLines, fsShellIfE+shellQuote(p.dstResolved)+fsShellThenSuffix+" echo 'destination already exists' >&2; exit 1; fi")
	}
	scriptLines = append(scriptLines, "mv "+shellQuote(p.srcResolved)+" "+shellQuote(p.dstResolved))

	commandResult, err := runShellCommand(ctx, runner, session, strings.Join(scriptLines, "\n"), nil, 64*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}
	return app.ToolRunResult{
		Output: map[string]any{
			fsKeySourcePath: filepath.Clean(p.sourcePath),
			fsKeyDestPath:   filepath.Clean(p.destinationPath),
			"moved":         true,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "moved path"}},
	}, nil
}

func (h *FSCopyHandler) Name() string {
	return "fs.copy"
}

func (h *FSCopyHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		SourcePath      string `json:"source_path"`
		DestinationPath string `json:"destination_path"`
		Recursive       bool   `json:"recursive"`
		Overwrite       bool   `json:"overwrite"`
		CreateParents   bool   `json:"create_parents"`
	}{CreateParents: true}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.copy args", Retryable: false}
	}
	if strings.TrimSpace(request.SourcePath) == "" || strings.TrimSpace(request.DestinationPath) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "source_path and destination_path are required", Retryable: false}
	}

	srcResolved, srcErr := resolvePath(session, request.SourcePath)
	if srcErr != nil {
		return app.ToolRunResult{}, srcErr
	}
	dstResolved, dstErr := resolvePath(session, request.DestinationPath)
	if dstErr != nil {
		return app.ToolRunResult{}, dstErr
	}
	if srcResolved == dstResolved {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "source and destination must differ", Retryable: false}
	}

	cp := fsCopyParams{
		sourcePath:      request.SourcePath,
		destinationPath: request.DestinationPath,
		srcResolved:     srcResolved,
		dstResolved:     dstResolved,
		recursive:       request.Recursive,
		overwrite:       request.Overwrite,
		createParents:   request.CreateParents,
	}
	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, cp)
	}
	return h.invokeLocal(cp)
}

type fsCopyParams struct {
	sourcePath      string
	destinationPath string
	srcResolved     string
	dstResolved     string
	recursive       bool
	overwrite       bool
	createParents   bool
}

func (h *FSCopyHandler) invokeLocal(p fsCopyParams) (app.ToolRunResult, *domain.Error) {
	srcInfo, err := os.Lstat(p.srcResolved)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "symbolic links are not supported by fs.copy", Retryable: false}
	}

	if prepErr := fsCopyPrepareDestination(p.dstResolved, p.createParents, p.overwrite); prepErr != nil {
		return app.ToolRunResult{}, prepErr
	}

	copiedType, copyErr := fsCopyDispatch(p.srcResolved, p.dstResolved, srcInfo, p.recursive)
	if copyErr != nil {
		return app.ToolRunResult{}, copyErr
	}

	return app.ToolRunResult{
		Output: map[string]any{
			fsKeySourcePath: filepath.Clean(p.sourcePath),
			fsKeyDestPath:   filepath.Clean(p.destinationPath),
			"copied":        true,
			"type":          copiedType,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "copied path"}},
	}, nil
}

// fsCopyPrepareDestination creates parent directories when requested and
// removes an existing destination when overwrite is allowed.
func fsCopyPrepareDestination(dstResolved string, createParents bool, overwrite bool) *domain.Error {
	if createParents {
		if err := os.MkdirAll(filepath.Dir(dstResolved), 0o755); err != nil {
			return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
		}
	}
	_, statErr := os.Stat(dstResolved)
	if statErr == nil {
		if !overwrite {
			return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "destination already exists", Retryable: false}
		}
		if removeErr := os.RemoveAll(dstResolved); removeErr != nil {
			return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: removeErr.Error(), Retryable: false}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: statErr.Error(), Retryable: false}
	}
	return nil
}

// fsCopyDispatch performs the actual file or directory copy and returns
// the copied type ("file" or "dir") and any error.
func fsCopyDispatch(srcResolved, dstResolved string, srcInfo os.FileInfo, recursive bool) (string, *domain.Error) {
	if srcInfo.IsDir() {
		if !recursive {
			return "", &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "recursive=true required for directory copy", Retryable: false}
		}
		if copyErr := copyDirectoryRecursive(srcResolved, dstResolved); copyErr != nil {
			return "", &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: copyErr.Error(), Retryable: false}
		}
		return "dir", nil
	}
	if copyErr := copyFileWithMode(srcResolved, dstResolved, srcInfo.Mode()); copyErr != nil {
		return "", &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: copyErr.Error(), Retryable: false}
	}
	return "file", nil
}

func (h *FSCopyHandler) invokeRemote(ctx context.Context, session domain.Session, p fsCopyParams) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	recursiveFlag := "0"
	if p.recursive {
		recursiveFlag = "1"
	}
	scriptLines := []string{
		fsShellIfNotE + shellQuote(p.srcResolved) + fsShellThenSuffix + " echo 'source path not found' >&2; exit 1; fi",
		fsShellIfD + shellQuote(p.srcResolved) + " ] && [ \"" + recursiveFlag + "\" != \"1\" ]; then echo 'recursive=true required for directory copy' >&2; exit 1; fi",
	}
	if p.createParents {
		scriptLines = append(scriptLines, fsShellMkdirP+shellQuote(filepath.Dir(p.dstResolved)))
	}
	if p.overwrite {
		scriptLines = append(scriptLines, fsShellIfE+shellQuote(p.dstResolved)+fsShellThenRmRf+shellQuote(p.dstResolved)+"; fi")
	} else {
		scriptLines = append(scriptLines, fsShellIfE+shellQuote(p.dstResolved)+fsShellThenSuffix+" echo 'destination already exists' >&2; exit 1; fi")
	}
	scriptLines = append(scriptLines, fsShellIfD+shellQuote(p.srcResolved)+fsShellThenSuffix+" cp -R "+shellQuote(p.srcResolved)+" "+shellQuote(p.dstResolved)+"; else cp "+shellQuote(p.srcResolved)+" "+shellQuote(p.dstResolved)+"; fi")

	commandResult, err := runShellCommand(ctx, runner, session, strings.Join(scriptLines, "\n"), nil, 64*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}
	return app.ToolRunResult{
		Output: map[string]any{
			fsKeySourcePath: filepath.Clean(p.sourcePath),
			fsKeyDestPath:   filepath.Clean(p.destinationPath),
			"copied":        true,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "copied path"}},
	}, nil
}

func (h *FSDeleteHandler) Name() string {
	return "fs.delete"
}

func (h *FSDeleteHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Force     bool   `json:"force"`
	}{}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.delete args", Retryable: false}
	}
	if strings.TrimSpace(request.Path) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}
	if filepath.Clean(resolved) == filepath.Clean(session.WorkspacePath) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodePolicyDenied, Message: "refusing to delete workspace root", Retryable: false}
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved, request.Recursive, request.Force)
	}
	return h.invokeLocal(request.Path, resolved, request.Recursive, request.Force)
}

func (h *FSDeleteHandler) invokeLocal(path, resolved string, recursive, force bool) (app.ToolRunResult, *domain.Error) {
	info, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && force {
			return app.ToolRunResult{
				Output: map[string]any{"path": filepath.Clean(path), "deleted": false, "existed": false},
				Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fsErrPathNotExist}},
			}, nil
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if info.IsDir() && !recursive {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "recursive=true required to delete directories", Retryable: false}
	}

	if info.IsDir() {
		err = os.RemoveAll(resolved)
	} else {
		err = os.Remove(resolved)
	}
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	return app.ToolRunResult{
		Output: map[string]any{"path": filepath.Clean(path), "deleted": true, "existed": true},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "deleted path"}},
	}, nil
}

func (h *FSDeleteHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	path string,
	resolved string,
	recursive bool,
	force bool,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	recursiveFlag := "0"
	if recursive {
		recursiveFlag = "1"
	}
	forceFlag := "0"
	if force {
		forceFlag = "1"
	}
	scriptLines := []string{
		fsShellIfNotE + shellQuote(resolved) + fsShellThenSuffix,
		"  if [ \"" + forceFlag + "\" = \"1\" ]; then exit 0; fi",
		"  echo 'path not found' >&2; exit 1",
		"fi",
		fsShellIfD + shellQuote(resolved) + " ] && [ \"" + recursiveFlag + "\" != \"1\" ]; then echo 'recursive=true required to delete directories' >&2; exit 1; fi",
		fsShellIfD + shellQuote(resolved) + fsShellThenRmRf + shellQuote(resolved) + "; else rm -f " + shellQuote(resolved) + "; fi",
	}
	commandResult, err := runShellCommand(ctx, runner, session, strings.Join(scriptLines, "\n"), nil, 64*1024)
	if err != nil {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}
	return app.ToolRunResult{
		Output: map[string]any{"path": filepath.Clean(path), "deleted": true},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "deleted path"}},
	}, nil
}

func (h *FSStatHandler) Name() string {
	return "fs.stat"
}

func (h *FSStatHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path string `json:"path"`
	}{}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.stat args", Retryable: false}
	}
	if strings.TrimSpace(request.Path) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: fsErrPathRequired, Retryable: false}
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request.Path, resolved)
	}
	return h.invokeLocal(request.Path, resolved)
}

func (h *FSStatHandler) invokeLocal(path, resolved string) (app.ToolRunResult, *domain.Error) {
	info, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return app.ToolRunResult{
				Output: map[string]any{"path": filepath.Clean(path), "exists": false},
				Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fsErrPathNotExist}},
			}, nil
		}
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"path":        filepath.Clean(path),
			"exists":      true,
			"type":          fsEntryType(info.Mode()),
			fsKeySizeBytes: info.Size(),
			"mode":          info.Mode().String(),
			fsKeyModifiedAt: info.ModTime().UTC(),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "stat collected"}},
	}, nil
}

func (h *FSStatHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	path string,
	resolved string,
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	script := strings.Join([]string{
		fsShellIfD + shellQuote(resolved) + fsShellThenSuffix + " t=dir; elif [ -f " + shellQuote(resolved) + " ]; then t=file; elif [ -e " + shellQuote(resolved) + " ]; then t=other; else echo 'missing'; exit 3; fi",
		"if [ \"$t\" = \"file\" ]; then sz=$(wc -c < " + shellQuote(resolved) + " | tr -d '[:space:]'); else sz=0; fi",
		"if out=$(stat -c '%a\\t%Y' " + shellQuote(resolved) + " 2>/dev/null); then perm=$(printf '%s' \"$out\" | cut -f1); mtime=$(printf '%s' \"$out\" | cut -f2); " +
			"elif out=$(stat -f '%Lp\\t%m' " + shellQuote(resolved) + " 2>/dev/null); then perm=$(printf '%s' \"$out\" | cut -f1); mtime=$(printf '%s' \"$out\" | cut -f2); " +
			"else perm=''; mtime=0; fi",
		"printf '%s\\t%s\\t%s\\t%s\\n' \"$t\" \"$sz\" \"$perm\" \"$mtime\"",
	}, "\n")
	commandResult, err := runShellCommand(ctx, runner, session, script, nil, 64*1024)
	if err != nil {
		if strings.Contains(commandResult.Output, "missing") {
			return app.ToolRunResult{
				Output: map[string]any{"path": filepath.Clean(path), "exists": false},
				Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fsErrPathNotExist}},
			}, nil
		}
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}

	line := ""
	for _, current := range splitOutputLines(commandResult.Output) {
		line = current
		break
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 4 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "unexpected fs.stat output", Retryable: false}
	}

	sizeValue, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	mtimeEpoch, _ := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
	modeString := strings.TrimSpace(parts[2])
	output := map[string]any{
		"path":       filepath.Clean(path),
		"exists":     true,
		"type":         strings.TrimSpace(parts[0]),
		fsKeySizeBytes: sizeValue,
	}
	if modeString != "" {
		output["mode"] = modeString
	}
	if mtimeEpoch > 0 {
		output[fsKeyModifiedAt] = time.Unix(mtimeEpoch, 0).UTC()
	}
	return app.ToolRunResult{
		Output: output,
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: "stat collected"}},
	}, nil
}

func (h *FSPatchHandler) Name() string {
	return "fs.patch"
}

func (h *FSPatchHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		UnifiedDiff string `json:"unified_diff"`
		Strategy    string `json:"strategy"`
	}{Strategy: fsKeyRejectConflict}

	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.patch args", Retryable: false}
	}
	if strings.TrimSpace(request.UnifiedDiff) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "unified_diff is required", Retryable: false}
	}

	strategy := strings.ToLower(strings.TrimSpace(request.Strategy))
	if strategy == "" {
		strategy = fsKeyRejectConflict
	}
	if strategy != "apply" && strategy != fsKeyRejectConflict {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "strategy must be apply or reject_on_conflict",
			Retryable: false,
		}
	}

	changedPaths, err := extractPatchPaths(request.UnifiedDiff)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid unified diff", Retryable: false}
	}
	for _, changedPath := range changedPaths {
		if !pathAllowed(changedPath, session.AllowedPaths) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   "patch touches paths outside allowed_paths",
				Retryable: false,
			}
		}
	}

	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}
	command := []string{"apply", "--whitespace=nowarn"}
	if strategy == "apply" {
		command = append(command, "--3way")
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "git",
		Args:     command,
		Stdin:    []byte(request.UnifiedDiff),
		MaxBytes: 1024 * 1024,
	})
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Output: map[string]any{
			"applied":       runErr == nil,
			"strategy":      strategy,
			"changed_paths": changedPaths,
			"output":        commandResult.Output,
		},
		Logs: []domain.LogLine{
			{At: time.Now().UTC(), Channel: fsKeyStdout, Message: commandResult.Output},
		},
	}
	if runErr != nil {
		return result, toFSRunnerError(runErr, commandResult.Output)
	}
	return result, nil
}

func (h *FSSearchHandler) Name() string {
	return "fs.search"
}

func (h *FSSearchHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path       string `json:"path"`
		Pattern    string `json:"pattern"`
		MaxResults int    `json:"max_results"`
	}{Path: ".", MaxResults: 200}

	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid fs.search args", Retryable: false}
	}
	if strings.TrimSpace(request.Pattern) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "pattern is required", Retryable: false}
	}
	if request.MaxResults <= 0 {
		request.MaxResults = 200
	}
	if request.MaxResults > 2000 {
		request.MaxResults = 2000
	}

	if isKubernetesRuntime(session) {
		return h.invokeRemote(ctx, session, request)
	}
	return h.invokeLocal(session, request)
}

func (h *FSSearchHandler) invokeLocal(
	session domain.Session,
	request struct {
		Path       string `json:"path"`
		Pattern    string `json:"pattern"`
		MaxResults int    `json:"max_results"`
	},
) (app.ToolRunResult, *domain.Error) {
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	re, err := regexp.Compile(request.Pattern)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: err.Error(), Retryable: false}
	}

	results, walkErr := fsSearchWalk(resolved, session.WorkspacePath, re, request.MaxResults)
	if walkErr != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: walkErr.Error(), Retryable: false}
	}

	return app.ToolRunResult{
		Output: map[string]any{"matches": results, "count": len(results)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("found %d matches", len(results))}},
	}, nil
}

// fsSearchWalk walks the directory tree rooted at resolved, scanning each
// regular file for matches against re. It stops early when maxResults is
// reached. The returned error is non-nil only for unexpected walk failures.
func fsSearchWalk(resolved, workspacePath string, re *regexp.Regexp, maxResults int) ([]fsSearchMatch, error) {
	results := make([]fsSearchMatch, 0, maxResults)
	walkStop := errors.New("search-limit-reached")
	walkErr := filepath.Walk(resolved, fsSearchWalkFunc(workspacePath, re, &results, maxResults, walkStop))
	if walkErr != nil && !errors.Is(walkErr, walkStop) {
		return nil, walkErr
	}
	return results, nil
}

// fsSearchWalkFunc returns the filepath.WalkFunc used by fsSearchWalk,
// keeping the callback logic in its own function to reduce cognitive complexity.
func fsSearchWalkFunc(workspacePath string, re *regexp.Regexp, results *[]fsSearchMatch, maxResults int, walkStop error) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		for _, m := range fsSearchScanFile(path, workspacePath, re) {
			*results = append(*results, m)
			if len(*results) >= maxResults {
				return walkStop
			}
		}
		return nil
	}
}

func (h *FSSearchHandler) invokeRemote(
	ctx context.Context,
	session domain.Session,
	request struct {
		Path       string `json:"path"`
		Pattern    string `json:"pattern"`
		MaxResults int    `json:"max_results"`
	},
) (app.ToolRunResult, *domain.Error) {
	runner, runErr := resolveKubernetesRunner(h.runner)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}
	resolved, pathErr := resolvePath(session, request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, pathErr
	}

	if _, err := regexp.Compile(request.Pattern); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: err.Error(), Retryable: false}
	}

	commandResult, err := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "grep",
		Args:     []string{"-R", "-n", "-E", "--", request.Pattern, resolved},
		MaxBytes: 2 * 1024 * 1024,
	})
	if err != nil && commandResult.ExitCode != 1 {
		return app.ToolRunResult{}, toFSRunnerError(err, commandResult.Output)
	}

	results := make([]fsSearchMatch, 0, request.MaxResults)
	for _, line := range splitOutputLines(commandResult.Output) {
		if len(results) >= request.MaxResults {
			break
		}
		path, lineNumber, snippet, ok := parseGrepLine(line)
		if !ok {
			continue
		}
		rel, relErr := filepath.Rel(session.WorkspacePath, path)
		if relErr != nil {
			rel = path
		}
		results = append(results, fsSearchMatch{
			Path:    rel,
			Line:    lineNumber,
			Snippet: snippet,
		})
	}

	return app.ToolRunResult{
		Output: map[string]any{"matches": results, "count": len(results)},
		Logs:   []domain.LogLine{{At: time.Now().UTC(), Channel: fsKeyStdout, Message: fmt.Sprintf("found %d matches", len(results))}},
	}, nil
}

// fsSearchMatch is the JSON-serialisable match record returned by fs.search.
type fsSearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// fsSearchScanFile scans a single file for lines matching re and returns all
// matches as fsSearchMatch values with paths relative to workspacePath.
func fsSearchScanFile(path, workspacePath string, re *regexp.Regexp) []fsSearchMatch {
	file, openErr := os.Open(path)
	if openErr != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)

	rel, relErr := filepath.Rel(workspacePath, path)
	if relErr != nil {
		rel = path
	}

	var matches []fsSearchMatch
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		if re.MatchString(text) {
			matches = append(matches, fsSearchMatch{Path: rel, Line: lineNum, Snippet: text})
		}
	}
	return matches
}

func parseGrepLine(line string) (string, int, string, bool) {
	firstColon := strings.Index(line, ":")
	if firstColon <= 0 {
		return "", 0, "", false
	}
	remaining := line[firstColon+1:]
	secondColon := strings.Index(remaining, ":")
	if secondColon <= 0 {
		return "", 0, "", false
	}
	path := line[:firstColon]
	lineNumberRaw := remaining[:secondColon]
	snippet := remaining[secondColon+1:]
	lineNumber, err := strconv.Atoi(lineNumberRaw)
	if err != nil {
		return "", 0, "", false
	}
	return path, lineNumber, snippet, true
}

func isKubernetesRuntime(session domain.Session) bool {
	return session.Runtime.Kind == domain.RuntimeKindKubernetes
}

func resolveKubernetesRunner(runner app.CommandRunner) (app.CommandRunner, *domain.Error) {
	if runner == nil {
		return nil, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "kubernetes fs handler requires command runner",
			Retryable: false,
		}
	}
	return runner, nil
}

func runShellCommand(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	script string,
	stdin []byte,
	maxBytes int,
) (app.CommandResult, error) {
	return runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "sh",
		Args:     []string{"-lc", script},
		Stdin:    stdin,
		MaxBytes: maxBytes,
	})
}

func toFSRunnerError(err error, output string) *domain.Error {
	if strings.Contains(err.Error(), "timeout") {
		return &domain.Error{
			Code:      app.ErrorCodeTimeout,
			Message:   "command timed out",
			Retryable: true,
		}
	}
	message := strings.TrimSpace(output)
	if message == "" {
		message = err.Error()
	}
	return &domain.Error{
		Code:      app.ErrorCodeExecutionFailed,
		Message:   message,
		Retryable: false,
	}
}

func splitOutputLines(output string) []string {
	raw := strings.Split(output, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func parseFSMkdirMode(raw string) (os.FileMode, *domain.Error) {
	modeText := strings.TrimSpace(raw)
	if modeText == "" {
		modeText = "0755"
	}
	modeText = strings.TrimPrefix(modeText, "0o")
	modeText = strings.TrimPrefix(modeText, "0O")
	if !strings.HasPrefix(modeText, "0") {
		modeText = "0" + modeText
	}
	parsed, err := strconv.ParseUint(modeText, 8, 32)
	if err != nil {
		return 0, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "mode must be an octal permission string (e.g. 0755)", Retryable: false}
	}
	return os.FileMode(parsed), nil
}

func formatPermission(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}

func fsEntryType(mode os.FileMode) string {
	if mode.IsDir() {
		return "dir"
	}
	if mode.IsRegular() {
		return "file"
	}
	if mode&os.ModeSymlink != 0 {
		return "symlink"
	}
	return "other"
}

func copyDirectoryRecursive(source, destination string) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("source is not a directory")
	}
	if err := os.MkdirAll(destination, sourceInfo.Mode().Perm()); err != nil {
		return err
	}

	return filepath.Walk(source, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destination, relPath)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported by fs.copy")
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFileWithMode(current, targetPath, info.Mode())
	})
}

func copyFileWithMode(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func extractPatchPaths(unifiedDiff string) ([]string, error) {
	lines := strings.Split(unifiedDiff, "\n")
	paths := []string{}
	seen := map[string]bool{}

	for _, line := range lines {
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
		if path == "" || path == "/dev/null" {
			continue
		}
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "b/")
		cleaned := filepath.Clean(path)
		if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			return nil, fmt.Errorf("unsafe patch path: %s", path)
		}
		if !seen[cleaned] {
			paths = append(paths, cleaned)
			seen[cleaned] = true
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no patch paths found")
	}
	return paths, nil
}
