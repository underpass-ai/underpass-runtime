package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testFSNotesTodo      = "notes/todo.txt"
	testFSKeyContent     = "content"
	testFSKeyPath        = "path"
	testFSKeyRecursive   = "recursive"
	testFSKeyCreateDirs  = "create_parents"
	testFSKeySrcPath     = "source_path"
	testFSKeyDstPath     = "destination_path"
	testFSKeyOverwrite   = "overwrite"
	testFSKeyUnifiedDiff = "unified_diff"
	testFSKeyEncoding    = "encoding"
	testFSKeyForce       = "force"
	testFSKeyPattern     = "pattern"
	testFSKeyExists      = "exists"
	testFSKeyCount       = "count"
	testFSTodoContent    = "hola\nTODO: test"
)

func TestFSWriteReadListSearchFlow(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	ctx := context.Background()

	write := &FSWriteHandler{}
	read := &FSReadHandler{}
	list := &FSListHandler{}
	search := &FSSearchHandler{}

	_, err := write.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:       testFSNotesTodo,
		testFSKeyContent:    testFSTodoContent,
		testFSKeyCreateDirs: true,
	}))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	readResult, err := read.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: testFSNotesTodo}))
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	readOutput := readResult.Output.(map[string]any)
	if readOutput[testFSKeyEncoding] != "utf8" {
		t.Fatalf("unexpected encoding: %#v", readOutput)
	}

	listResult, err := list.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: ".", testFSKeyRecursive: true}))
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if listResult.Output.(map[string]any)[testFSKeyCount].(int) < 1 {
		t.Fatalf("expected at least one entry, got %#v", listResult.Output)
	}

	searchResult, err := search.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: ".", testFSKeyPattern: "TODO"}))
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}
	if searchResult.Output.(map[string]any)[testFSKeyCount].(int) < 1 {
		t.Fatalf("expected at least one match, got %#v", searchResult.Output)
	}
}

func TestFSReadBinaryAndValidationErrors(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatalf("write binary file failed: %v", err)
	}

	read := &FSReadHandler{}
	result, err := read.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "bin.dat"}))
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[testFSKeyEncoding] != "base64" {
		t.Fatalf("expected base64 encoding, got %#v", output[testFSKeyEncoding])
	}
	if _, decodeErr := base64.StdEncoding.DecodeString(output[testFSKeyContent].(string)); decodeErr != nil {
		t.Fatalf("expected valid base64 content: %v", decodeErr)
	}

	_, err = read.Invoke(ctx, session, mustJSON(t, map[string]any{}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected missing path error, got: %#v", err)
	}

	search := &FSSearchHandler{}
	_, err = search.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPattern: "["}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid regex error, got: %#v", err)
	}
}

func TestFSWriteValidationAndTraversal(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	ctx := context.Background()
	write := &FSWriteHandler{}

	_, err := write.Invoke(ctx, session, json.RawMessage(`{"path":"x","content":"%%%","encoding":"base64"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid base64 error, got: %#v", err)
	}

	_, err = write.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "x", testFSKeyContent: "a", testFSKeyEncoding: "unknown"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid encoding error, got: %#v", err)
	}

	_, err = write.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "../x", testFSKeyContent: "a"}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy denial, got: %#v", err)
	}

	large := make([]byte, 1024*1024+1)
	_, err = write.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "big.txt", testFSKeyContent: string(large)}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected content size error, got: %#v", err)
	}
}

func TestFSHandlers_KubernetesRuntimeUsesCommandRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: testWorkspaceRepoPath,
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	ctx := context.Background()

	runner := &fakeFSCommandRunner{}
	runner.run = func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
		if spec.Command == "grep" {
			return app.CommandResult{
				Output:   "/workspace/repo/notes/todo.txt:2:TODO: test\n",
				ExitCode: 0,
			}, nil
		}
		if spec.Command != "sh" || len(spec.Args) < 2 {
			return app.CommandResult{}, fmt.Errorf("unexpected command: %s %v", spec.Command, spec.Args)
		}
		script := spec.Args[1]
		switch {
		case strings.Contains(script, "cat > '/workspace/repo/notes/todo.txt'"):
			return app.CommandResult{Output: "", ExitCode: 0}, nil
		case strings.Contains(script, "dd if='/workspace/repo/notes/todo.txt'"):
			return app.CommandResult{Output: base64.StdEncoding.EncodeToString([]byte(testFSTodoContent)), ExitCode: 0}, nil
		case strings.Contains(script, "find '/workspace/repo' -mindepth 1"):
			return app.CommandResult{
				Output:   "dir\t/workspace/repo/notes\nfile\t/workspace/repo/notes/todo.txt\n",
				ExitCode: 0,
			}, nil
		default:
			return app.CommandResult{}, fmt.Errorf("unexpected shell script: %s", script)
		}
	}

	write := NewFSWriteHandler(runner)
	read := NewFSReadHandler(runner)
	list := NewFSListHandler(runner)
	search := NewFSSearchHandler(runner)

	_, err := write.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:       testFSNotesTodo,
		testFSKeyContent:    testFSTodoContent,
		testFSKeyCreateDirs: true,
	}))
	if err != nil {
		t.Fatalf("unexpected kubernetes write error: %v", err)
	}

	readResult, err := read.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: testFSNotesTodo}))
	if err != nil {
		t.Fatalf("unexpected kubernetes read error: %v", err)
	}
	if content := readResult.Output.(map[string]any)[testFSKeyContent].(string); content != testFSTodoContent {
		t.Fatalf("unexpected kubernetes read content: %q", content)
	}

	listResult, err := list.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: ".", testFSKeyRecursive: true}))
	if err != nil {
		t.Fatalf("unexpected kubernetes list error: %v", err)
	}
	if listResult.Output.(map[string]any)[testFSKeyCount].(int) < 1 {
		t.Fatalf("expected kubernetes list entries, got %#v", listResult.Output)
	}

	searchResult, err := search.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: ".", testFSKeyPattern: "TODO"}))
	if err != nil {
		t.Fatalf("unexpected kubernetes search error: %v", err)
	}
	if searchResult.Output.(map[string]any)[testFSKeyCount].(int) != 1 {
		t.Fatalf("unexpected kubernetes search output: %#v", searchResult.Output)
	}
}

func TestFSHandlers_KubernetesRuntimeRequiresRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: testWorkspaceRepoPath,
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, err := NewFSReadHandler(nil).Invoke(context.Background(), session, mustJSON(t, map[string]any{testFSKeyPath: "notes/a.txt"}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected missing runner execution error, got %#v", err)
	}
}

func TestFSPatchHandler_ValidationAndExecution(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"src"}}
	handler := NewFSPatchHandler(nil)

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"unified_diff":""}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected unified_diff required error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, json.RawMessage(`{"unified_diff":"@@ bad","strategy":"invalid"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected strategy validation error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustJSON(t, map[string]any{
		testFSKeyUnifiedDiff: strings.Join([]string{
			"diff --git a/outside.txt b/outside.txt",
			"--- a/outside.txt",
			"+++ b/outside.txt",
			"@@ -1 +1 @@",
			"-hello",
			"+hola",
		}, "\n"),
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected patch path policy denial, got %#v", err)
	}
}

func TestFSPatchHandler_UsesRunnerAndStrategy(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/src/a.txt b/src/a.txt",
		"--- a/src/a.txt",
		"+++ b/src/a.txt",
		"@@ -1 +1 @@",
		"-hello",
		"+hola",
	}, "\n")
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"src"}}

	runner := &fakeFSCommandRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "git" || len(spec.Args) < 2 || spec.Args[0] != "apply" {
				t.Fatalf("unexpected patch command: %s %v", spec.Command, spec.Args)
			}
			if string(spec.Stdin) != diff {
				t.Fatalf("unexpected patch stdin: %q", string(spec.Stdin))
			}
			return app.CommandResult{ExitCode: 0, Output: "applied"}, nil
		},
	}
	handler := NewFSPatchHandler(runner)

	result, err := handler.Invoke(context.Background(), session, mustJSON(t, map[string]any{
		testFSKeyUnifiedDiff: diff,
		"strategy":           "apply",
	}))
	if err != nil {
		t.Fatalf("unexpected fs.patch error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["applied"] != true {
		t.Fatalf("expected applied=true, got %#v", output["applied"])
	}
}

func TestFSPatchHandler_MapsRunnerErrors(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/src/a.txt b/src/a.txt",
		"--- a/src/a.txt",
		"+++ b/src/a.txt",
		"@@ -1 +1 @@",
		"-hello",
		"+hola",
	}, "\n")
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"src"}}

	timeoutRunner := &fakeFSCommandRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 124, Output: "timed out"}, fmt.Errorf("command timeout")
		},
	}
	_, err := NewFSPatchHandler(timeoutRunner).Invoke(context.Background(), session, mustJSON(t, map[string]any{
		testFSKeyUnifiedDiff: diff,
	}))
	if err == nil || err.Code != app.ErrorCodeTimeout {
		t.Fatalf("expected timeout mapping, got %#v", err)
	}

	execRunner := &fakeFSCommandRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "patch failed"}, fmt.Errorf("exit 1")
		},
	}
	_, err = NewFSPatchHandler(execRunner).Invoke(context.Background(), session, mustJSON(t, map[string]any{
		testFSKeyUnifiedDiff: diff,
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution failure mapping, got %#v", err)
	}
}

func TestFSOps_LocalLifecycle(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	ctx := context.Background()

	mkdir := NewFSMkdirHandler(nil)
	move := NewFSMoveHandler(nil)
	copyHandler := NewFSCopyHandler(nil)
	deleteHandler := NewFSDeleteHandler(nil)
	stat := NewFSStatHandler(nil)
	write := NewFSWriteHandler(nil)

	_, err := mkdir.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:       "tmp/work",
		testFSKeyCreateDirs: true,
		"mode":              "0755",
	}))
	if err != nil {
		t.Fatalf("unexpected fs.mkdir error: %#v", err)
	}

	_, err = write.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:       "tmp/work/input.txt",
		testFSKeyContent:    "payload",
		testFSKeyCreateDirs: true,
	}))
	if err != nil {
		t.Fatalf("unexpected fs.write_file error: %#v", err)
	}

	_, err = copyHandler.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath:   "tmp/work/input.txt",
		testFSKeyDstPath:   "tmp/work/input.copy.txt",
		testFSKeyOverwrite: true,
	}))
	if err != nil {
		t.Fatalf("unexpected fs.copy error: %#v", err)
	}

	_, err = move.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath:    "tmp/work/input.copy.txt",
		testFSKeyDstPath:    "tmp/archive/input.copy.txt",
		testFSKeyCreateDirs: true,
		testFSKeyOverwrite:  true,
	}))
	if err != nil {
		t.Fatalf("unexpected fs.move error: %#v", err)
	}

	statResult, err := stat.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "tmp/archive/input.copy.txt"}))
	if err != nil {
		t.Fatalf("unexpected fs.stat error: %#v", err)
	}
	statOutput := statResult.Output.(map[string]any)
	if exists, ok := statOutput[testFSKeyExists].(bool); !ok || !exists {
		t.Fatalf("expected fs.stat exists=true, got %#v", statOutput)
	}
	if statOutput["type"] != "file" {
		t.Fatalf("expected fs.stat type=file, got %#v", statOutput["type"])
	}

	_, err = deleteHandler.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:      "tmp/archive/input.copy.txt",
		testFSKeyRecursive: false,
		testFSKeyForce:     false,
	}))
	if err != nil {
		t.Fatalf("unexpected fs.delete error: %#v", err)
	}

	missingResult, err := stat.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "tmp/archive/input.copy.txt"}))
	if err != nil {
		t.Fatalf("unexpected fs.stat after delete error: %#v", err)
	}
	missingOutput := missingResult.Output.(map[string]any)
	if exists, ok := missingOutput[testFSKeyExists].(bool); !ok || exists {
		t.Fatalf("expected fs.stat exists=false after delete, got %#v", missingOutput)
	}
}

func TestFSOps_ValidationAndPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir fixture failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write fixture failed: %v", err)
	}

	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"dir"}}
	ctx := context.Background()

	_, err := NewFSMkdirHandler(nil).Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath: "dir/new",
		"mode":        "invalid",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected mode validation error, got %#v", err)
	}

	_, err = NewFSCopyHandler(nil).Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath:   "dir",
		testFSKeyDstPath:   "dir/dir-copy",
		testFSKeyRecursive: false,
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected recursive required error, got %#v", err)
	}

	_, err = NewFSMoveHandler(nil).Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath: "../outside",
		testFSKeyDstPath: "dir/b.txt",
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected traversal denial on move, got %#v", err)
	}

	_, err = NewFSDeleteHandler(nil).Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:      "dir",
		testFSKeyRecursive: false,
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected recursive required for directory delete, got %#v", err)
	}

	rootSession := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	_, err = NewFSDeleteHandler(nil).Invoke(ctx, rootSession, mustJSON(t, map[string]any{
		testFSKeyPath:      ".",
		testFSKeyRecursive: true,
		testFSKeyForce:     true,
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected workspace root delete denial, got %#v", err)
	}
}

func TestFSOps_KubernetesRuntimeUsesCommandRunner(t *testing.T) {
	session := domain.Session{
		WorkspacePath: testWorkspaceRepoPath,
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	ctx := context.Background()

	runner := &fakeFSCommandRunner{}
	runner.run = func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
		if spec.Command != "sh" || len(spec.Args) < 2 {
			return app.CommandResult{}, fmt.Errorf("unexpected command: %s %v", spec.Command, spec.Args)
		}
		script := spec.Args[1]
		switch {
		case strings.Contains(script, "mkdir -p '/workspace/repo/tmp/k8s'"):
			return app.CommandResult{Output: "", ExitCode: 0}, nil
		case strings.Contains(script, "cp '/workspace/repo/notes/todo.txt' '/workspace/repo/notes/todo.copy.txt'"):
			return app.CommandResult{Output: "", ExitCode: 0}, nil
		case strings.Contains(script, "mv '/workspace/repo/notes/todo.copy.txt' '/workspace/repo/notes/todo.moved.txt'"):
			return app.CommandResult{Output: "", ExitCode: 0}, nil
		case strings.Contains(script, "printf '%s\\t%s\\t%s\\t%s\\n'"):
			return app.CommandResult{Output: "file\t7\t0644\t1700000000\n", ExitCode: 0}, nil
		case strings.Contains(script, "rm -f '/workspace/repo/notes/todo.moved.txt'"):
			return app.CommandResult{Output: "", ExitCode: 0}, nil
		default:
			return app.CommandResult{}, fmt.Errorf("unexpected shell script: %s", script)
		}
	}

	mkdir := NewFSMkdirHandler(runner)
	copyHandler := NewFSCopyHandler(runner)
	move := NewFSMoveHandler(runner)
	stat := NewFSStatHandler(runner)
	deleteHandler := NewFSDeleteHandler(runner)

	_, err := mkdir.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "tmp/k8s", testFSKeyCreateDirs: true}))
	if err != nil {
		t.Fatalf("unexpected kubernetes fs.mkdir error: %#v", err)
	}

	_, err = copyHandler.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath:   testFSNotesTodo,
		testFSKeyDstPath:   "notes/todo.copy.txt",
		testFSKeyOverwrite: true,
	}))
	if err != nil {
		t.Fatalf("unexpected kubernetes fs.copy error: %#v", err)
	}

	_, err = move.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeySrcPath:   "notes/todo.copy.txt",
		testFSKeyDstPath:   "notes/todo.moved.txt",
		testFSKeyOverwrite: true,
	}))
	if err != nil {
		t.Fatalf("unexpected kubernetes fs.move error: %#v", err)
	}

	statResult, err := stat.Invoke(ctx, session, mustJSON(t, map[string]any{testFSKeyPath: "notes/todo.moved.txt"}))
	if err != nil {
		t.Fatalf("unexpected kubernetes fs.stat error: %#v", err)
	}
	if statResult.Output.(map[string]any)[testFSKeyExists] != true {
		t.Fatalf("expected kubernetes fs.stat exists=true, got %#v", statResult.Output)
	}

	_, err = deleteHandler.Invoke(ctx, session, mustJSON(t, map[string]any{
		testFSKeyPath:  "notes/todo.moved.txt",
		testFSKeyForce: true,
	}))
	if err != nil {
		t.Fatalf("unexpected kubernetes fs.delete error: %#v", err)
	}
}

type fakeFSCommandRunner struct {
	run func(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeFSCommandRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if f.run == nil {
		return app.CommandResult{}, fmt.Errorf("fake runner not configured")
	}
	return f.run(ctx, session, spec)
}

func mustJSON(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return data
}
