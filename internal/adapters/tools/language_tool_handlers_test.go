package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeLanguageCommandRunner struct {
	calls []app.CommandSpec
	run   func(callIndex int, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeLanguageCommandRunner) Run(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(len(f.calls)-1, spec)
	}
	return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
}

func TestRustBuildHandler_BuildsExpectedCommand(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	handler := NewRustBuildHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"target":  "workspace-crate",
		"release": true,
	}))
	if err != nil {
		t.Fatalf("unexpected rust.build error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	got := runner.calls[0]
	wantArgs := []string{"build", "--package", "workspace-crate", "--release"}
	if got.Command != "cargo" {
		t.Fatalf("expected cargo command, got %q", got.Command)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected rust.build args: got=%v want=%v", got.Args, wantArgs)
	}
}

func TestNodeInstallHandler_UsesInstallWhenUseCIFalse(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	handler := NewNodeInstallHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"use_ci":         false,
		"ignore_scripts": true,
	}))
	if err != nil {
		t.Fatalf("unexpected node.install error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	got := runner.calls[0]
	wantArgs := []string{"install", "--ignore-scripts"}
	if got.Command != "npm" {
		t.Fatalf("expected npm command, got %q", got.Command)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected node.install args: got=%v want=%v", got.Args, wantArgs)
	}
}

func TestNodeTypecheckHandler_AppendsTargetAfterDoubleDash(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	handler := NewNodeTypecheckHandler(runner)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"target": "packages/web",
	}))
	if err != nil {
		t.Fatalf("unexpected node.typecheck error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	got := runner.calls[0]
	wantArgs := []string{"run", "typecheck", "--if-present", "--", "packages/web"}
	if got.Command != "npm" {
		t.Fatalf("expected npm command, got %q", got.Command)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected node.typecheck args: got=%v want=%v", got.Args, wantArgs)
	}
}

func TestCBuildHandler_CompilesRequestedSource(t *testing.T) {
	root := t.TempDir()
	mainC := filepath.Join(root, "main.c")
	if err := os.WriteFile(mainC, []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}

	runner := &fakeLanguageCommandRunner{}
	handler := NewCBuildHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"source":      "main.c",
		"output_name": "todo-c",
		"standard":    "c11",
	}))
	if err != nil {
		t.Fatalf("unexpected c.build error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	got := runner.calls[0]
	wantArgs := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-o", "todo-c", "main.c"}
	if got.Command != "cc" {
		t.Fatalf("expected cc command, got %q", got.Command)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected c.build args: got=%v want=%v", got.Args, wantArgs)
	}
}

func TestCTestHandler_CompilesAndExecutesBinary(t *testing.T) {
	root := t.TempDir()
	testC := filepath.Join(root, "todo_test.c")
	if err := os.WriteFile(testC, []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write todo_test.c failed: %v", err)
	}

	runner := &fakeLanguageCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			if callIndex == 0 {
				return app.CommandResult{ExitCode: 0, Output: "compile ok"}, nil
			}
			if callIndex == 1 {
				return app.CommandResult{ExitCode: 0, Output: "tests ok"}, nil
			}
			return app.CommandResult{ExitCode: 1, Output: "unexpected call"}, nil
		},
	}
	handler := NewCTestHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"source":      "todo_test.c",
		"output_name": "todo-c-test",
		"standard":    "c11",
		"run":         true,
	}))
	if err != nil {
		t.Fatalf("unexpected c.test error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two runner calls, got %d", len(runner.calls))
	}

	compileCall := runner.calls[0]
	execCall := runner.calls[1]
	wantCompileArgs := []string{"-std=c11", "-O0", "-g", "-Wall", "-Wextra", "-o", "todo-c-test", "todo_test.c"}
	if compileCall.Command != "cc" {
		t.Fatalf("expected cc compile command, got %q", compileCall.Command)
	}
	if !reflect.DeepEqual(compileCall.Args, wantCompileArgs) {
		t.Fatalf("unexpected c.test compile args: got=%v want=%v", compileCall.Args, wantCompileArgs)
	}
	if execCall.Command != "./todo-c-test" {
		t.Fatalf("expected execution command ./todo-c-test, got %q", execCall.Command)
	}
}

func TestLanguageHandlerNamesAndConstructors(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	cases := []struct {
		want string
		got  string
	}{
		{want: "rust.build", got: NewRustBuildHandler(runner).Name()},
		{want: "rust.test", got: NewRustTestHandler(runner).Name()},
		{want: "rust.clippy", got: NewRustClippyHandler(runner).Name()},
		{want: "rust.format", got: NewRustFormatHandler(runner).Name()},
		{want: "node.install", got: NewNodeInstallHandler(runner).Name()},
		{want: "node.build", got: NewNodeBuildHandler(runner).Name()},
		{want: "node.test", got: NewNodeTestHandler(runner).Name()},
		{want: "node.lint", got: NewNodeLintHandler(runner).Name()},
		{want: "node.typecheck", got: NewNodeTypecheckHandler(runner).Name()},
		{want: "python.install_deps", got: NewPythonInstallDepsHandler(runner).Name()},
		{want: "python.validate", got: NewPythonValidateHandler(runner).Name()},
		{want: "python.test", got: NewPythonTestHandler(runner).Name()},
		{want: "c.build", got: NewCBuildHandler(runner).Name()},
		{want: "c.test", got: NewCTestHandler(runner).Name()},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("unexpected handler name: got=%q want=%q", tc.got, tc.want)
		}
	}
}

func TestRustHandlers_Commands(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	_, err := NewRustTestHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"target": "crate-a"}))
	if err != nil {
		t.Fatalf("rust.test failed: %v", err)
	}
	_, err = NewRustClippyHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"deny_warnings": true}))
	if err != nil {
		t.Fatalf("rust.clippy failed: %v", err)
	}
	_, err = NewRustFormatHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"check": true}))
	if err != nil {
		t.Fatalf("rust.format failed: %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.calls))
	}
	if runner.calls[0].Command != "cargo" || runner.calls[0].Args[0] != "test" {
		t.Fatalf("unexpected rust.test command: %#v", runner.calls[0])
	}
	if runner.calls[1].Command != "cargo" || runner.calls[1].Args[0] != "clippy" {
		t.Fatalf("unexpected rust.clippy command: %#v", runner.calls[1])
	}
	if runner.calls[2].Command != "cargo" || runner.calls[2].Args[0] != "fmt" {
		t.Fatalf("unexpected rust.format command: %#v", runner.calls[2])
	}
}

func TestNodeHandlers_Commands(t *testing.T) {
	runner := &fakeLanguageCommandRunner{}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	_, err := NewNodeBuildHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"target": "apps/web"}))
	if err != nil {
		t.Fatalf("node.build failed: %v", err)
	}
	_, err = NewNodeTestHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"target": "apps/web"}))
	if err != nil {
		t.Fatalf("node.test failed: %v", err)
	}
	_, err = NewNodeLintHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"target": "apps/web"}))
	if err != nil {
		t.Fatalf("node.lint failed: %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.calls))
	}
	if runner.calls[0].Command != "npm" || runner.calls[0].Args[1] != "build" {
		t.Fatalf("unexpected node.build args: %#v", runner.calls[0].Args)
	}
	if runner.calls[1].Command != "npm" || runner.calls[1].Args[1] != "test" {
		t.Fatalf("unexpected node.test args: %#v", runner.calls[1].Args)
	}
	if runner.calls[2].Command != "npm" || runner.calls[2].Args[1] != "lint" {
		t.Fatalf("unexpected node.lint args: %#v", runner.calls[2].Args)
	}
}

func TestPythonValidateAndTestHandlers(t *testing.T) {
	root := t.TempDir()
	runner := &fakeLanguageCommandRunner{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	_, err := NewPythonValidateHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"target": "src"}))
	if err != nil {
		t.Fatalf("python.validate failed: %v", err)
	}
	_, err = NewPythonTestHandler(runner).Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"target":      "tests",
		"run_pattern": "todo",
		"max_fail":    3,
	}))
	if err != nil {
		t.Fatalf("python.test failed: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(runner.calls))
	}
	if runner.calls[0].Command != "python3" || runner.calls[0].Args[0] != "-m" {
		t.Fatalf("unexpected python.validate call: %#v", runner.calls[0])
	}
	if runner.calls[1].Command != "pytest" {
		t.Fatalf("unexpected python.test executable: %q", runner.calls[1].Command)
	}
	if !reflect.DeepEqual(runner.calls[1].Args[:3], []string{"-q", "--maxfail", "3"}) {
		t.Fatalf("unexpected python.test args prefix: %#v", runner.calls[1].Args)
	}
}

func TestPythonInstallDepsHandler_ValidationAndInstallPaths(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	handler := NewPythonInstallDepsHandler(&fakeLanguageCommandRunner{})

	_, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"use_venv": false}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected use_venv validation error, got %#v", err)
	}

	runnerNoReq := &fakeLanguageCommandRunner{}
	noReqHandler := NewPythonInstallDepsHandler(runnerNoReq)
	result, err := noReqHandler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("python.install_deps no-req path failed: %#v", err)
	}
	if len(runnerNoReq.calls) != 1 {
		t.Fatalf("expected only venv setup call, got %d", len(runnerNoReq.calls))
	}
	output := result.Output.(map[string]any)
	if _, ok := output["diagnostics"]; !ok {
		t.Fatalf("expected diagnostics in no-requirements path, got %#v", output)
	}

	if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("pytest==8.0.0\n"), 0o644); err != nil {
		t.Fatalf("write requirements.txt failed: %v", err)
	}
	runnerWithReq := &fakeLanguageCommandRunner{}
	withReqHandler := NewPythonInstallDepsHandler(runnerWithReq)
	_, err = withReqHandler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{
		"use_venv": true,
	}))
	if err != nil {
		t.Fatalf("python.install_deps requirements path failed: %#v", err)
	}
	if len(runnerWithReq.calls) != 2 {
		t.Fatalf("expected 2 calls (venv + pip), got %d", len(runnerWithReq.calls))
	}
	if runnerWithReq.calls[1].Command != ".workspace-venv/bin/python" {
		t.Fatalf("unexpected python install executable: %q", runnerWithReq.calls[1].Command)
	}
}

func TestPythonTestHandler_InvalidPattern(t *testing.T) {
	handler := NewPythonTestHandler(&fakeLanguageCommandRunner{})
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	_, err := handler.Invoke(context.Background(), session, mustLanguageJSON(t, map[string]any{"run_pattern": "bad\x00pattern"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid run_pattern error, got %#v", err)
	}
}

func TestResolvePythonExecutablesFromVenv(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".workspace-venv", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir venv bin failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".workspace-venv", "bin", "python"), []byte(""), 0o755); err != nil {
		t.Fatalf("write python failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".workspace-venv", "bin", "pytest"), []byte(""), 0o755); err != nil {
		t.Fatalf("write pytest failed: %v", err)
	}

	if got := resolvePythonExecutable(root); got != ".workspace-venv/bin/python" {
		t.Fatalf("unexpected resolvePythonExecutable: %q", got)
	}
	if got := resolvePytestExecutable(root); got != ".workspace-venv/bin/pytest" {
		t.Fatalf("unexpected resolvePytestExecutable: %q", got)
	}
}

func mustLanguageJSON(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return data
}
