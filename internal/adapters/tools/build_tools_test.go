package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestRepoStaticAnalysisHandler_Go(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{}
	handler := NewRepoStaticAnalysisHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":"./..."}`))
	if err != nil {
		t.Fatalf("unexpected repo.static_analysis error: %#v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}
	if runner.calls[0].Command != "go" {
		t.Fatalf("expected go command, got %q", runner.calls[0].Command)
	}
	if len(runner.calls[0].Args) < 2 || runner.calls[0].Args[0] != "vet" || runner.calls[0].Args[1] != "./..." {
		t.Fatalf("unexpected args: %#v", runner.calls[0].Args)
	}
}

func TestRepoPackageHandler_C(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.c"), []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{}
	handler := NewRepoPackageHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected repo.package error: %#v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 runner calls, got %d", len(runner.calls))
	}
	if runner.calls[0].Command != "mkdir" {
		t.Fatalf("expected mkdir call, got %q", runner.calls[0].Command)
	}
	if runner.calls[1].Command != "cc" {
		t.Fatalf("expected cc call, got %q", runner.calls[1].Command)
	}

	output := result.Output.(map[string]any)
	if output["artifact_path"] != ".workspace-dist/c-app" {
		t.Fatalf("unexpected artifact_path: %#v", output["artifact_path"])
	}
}

func TestRepoStaticAnalysisHandler_RunErrorMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "vet failed"}, errors.New("exit 1")
		},
	}
	_, err := NewRepoStaticAnalysisHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{"target":"./..."}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected repo.static_analysis execution error, got %#v", err)
	}
}

func TestRepoPackageHandler_NodeArtifactDetection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "npm" {
				t.Fatalf("expected npm pack command, got %q", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: "demo-1.0.0.tgz"}, nil
		},
	}

	result, err := NewRepoPackageHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected repo.package node error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["artifact_path"] != "demo-1.0.0.tgz" {
		t.Fatalf("unexpected artifact_path: %#v", output["artifact_path"])
	}
}

func TestRepoPackageHandler_MkdirFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			if callIndex == 0 && spec.Command == "mkdir" {
				return app.CommandResult{ExitCode: 1, Output: "mkdir failed"}, errors.New("exit 1")
			}
			return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
		},
	}
	_, err := NewRepoPackageHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected mkdir failure mapping, got %#v", err)
	}
}

func TestStaticAnalysisCommandForProject_AllToolchains(t *testing.T) {
	workspaceC := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceC, "main.c"), []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}
	workspacePy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspacePy, ".workspace-venv", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir .workspace-venv failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePy, ".workspace-venv", "bin", "python"), []byte(""), 0o755); err != nil {
		t.Fatalf("write python binary failed: %v", err)
	}

	tests := []struct {
		name      string
		workspace string
		detected  projectType
		target    string
		command   string
		minArgs   int
	}{
		{name: "go", workspace: t.TempDir(), detected: projectType{Name: "go"}, target: "", command: "go", minArgs: 2},
		{name: "rust", workspace: t.TempDir(), detected: projectType{Name: "rust"}, target: "", command: "cargo", minArgs: 2},
		{name: "node", workspace: t.TempDir(), detected: projectType{Name: "node"}, target: "pkg/web", command: "npm", minArgs: 4},
		{name: "python", workspace: workspacePy, detected: projectType{Name: "python"}, target: ".", command: ".workspace-venv/bin/python", minArgs: 3},
		{name: "java-gradle", workspace: t.TempDir(), detected: projectType{Name: "java", Flavor: "gradle"}, target: "", command: "gradle", minArgs: 2},
		{name: "java-maven", workspace: t.TempDir(), detected: projectType{Name: "java"}, target: "", command: "mvn", minArgs: 3},
		{name: "c", workspace: workspaceC, detected: projectType{Name: "c"}, target: "", command: "cc", minArgs: 3},
	}

	for _, tc := range tests {
		command, args, err := staticAnalysisCommandForProject(tc.workspace, tc.detected, tc.target)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if command != tc.command {
			t.Fatalf("%s: unexpected command %q", tc.name, command)
		}
		if len(args) < tc.minArgs {
			t.Fatalf("%s: unexpected args: %#v", tc.name, args)
		}
	}

	_, _, err := staticAnalysisCommandForProject(t.TempDir(), projectType{Name: "unknown"}, "")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for unsupported toolchain, got %v", err)
	}
}

func TestPackageCommandForProject_AllToolchains(t *testing.T) {
	workspaceC := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceC, "app.c"), []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write app.c failed: %v", err)
	}
	workspacePy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspacePy, ".workspace-venv", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir .workspace-venv failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePy, ".workspace-venv", "bin", "python"), []byte(""), 0o755); err != nil {
		t.Fatalf("write python binary failed: %v", err)
	}

	tests := []struct {
		name       string
		workspace  string
		detected   projectType
		target     string
		command    string
		wantEnsure bool
		wantPath   string
	}{
		{name: "go", workspace: t.TempDir(), detected: projectType{Name: "go"}, target: "", command: "go", wantEnsure: true, wantPath: ".workspace-dist/app"},
		{name: "rust", workspace: t.TempDir(), detected: projectType{Name: "rust"}, target: "core", command: "cargo", wantEnsure: false, wantPath: "target/release"},
		{name: "node", workspace: t.TempDir(), detected: projectType{Name: "node"}, target: "", command: "npm", wantEnsure: false, wantPath: ""},
		{name: "python", workspace: workspacePy, detected: projectType{Name: "python"}, target: "", command: ".workspace-venv/bin/python", wantEnsure: true, wantPath: ".workspace-dist"},
		{name: "java-gradle", workspace: t.TempDir(), detected: projectType{Name: "java", Flavor: "gradle"}, target: "", command: "gradle", wantEnsure: false, wantPath: "build/libs"},
		{name: "java-maven", workspace: t.TempDir(), detected: projectType{Name: "java"}, target: "", command: "mvn", wantEnsure: false, wantPath: "target"},
		{name: "c", workspace: workspaceC, detected: projectType{Name: "c"}, target: "app.c", command: "cc", wantEnsure: true, wantPath: ".workspace-dist/c-app"},
	}

	for _, tc := range tests {
		command, args, artifactPath, ensureDist, err := packageCommandForProject(tc.workspace, tc.detected, tc.target)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if command != tc.command {
			t.Fatalf("%s: unexpected command %q", tc.name, command)
		}
		if len(args) == 0 {
			t.Fatalf("%s: expected non-empty args", tc.name)
		}
		if artifactPath != tc.wantPath {
			t.Fatalf("%s: unexpected artifact path %q", tc.name, artifactPath)
		}
		if ensureDist != tc.wantEnsure {
			t.Fatalf("%s: unexpected ensureDist=%v", tc.name, ensureDist)
		}
	}

	_, _, _, _, err := packageCommandForProject(t.TempDir(), projectType{Name: "c"}, "missing.txt")
	if err == nil {
		t.Fatal("expected missing c source error")
	}
}

func TestRuntimeArtifactAndPURLHelpers(t *testing.T) {
	packOutput := "npm notice package-size: 10 kB\nworkspace-demo-1.0.0.tgz\n"
	if got := detectNodePackageArtifact(packOutput); got != "workspace-demo-1.0.0.tgz" {
		t.Fatalf("unexpected package artifact: %q", got)
	}
	if got := dependencyPURL(dependencyEntry{Name: "github.com/foo/bar", Version: "1.0.0", Ecosystem: "go"}); got == "" || !strings.HasPrefix(got, "pkg:golang/") {
		t.Fatalf("unexpected go purl: %q", got)
	}
	if got := dependencyPURL(dependencyEntry{Name: "serde", Version: "1.0.0", Ecosystem: "rust"}); !strings.HasPrefix(got, "pkg:cargo/") {
		t.Fatalf("unexpected rust purl: %q", got)
	}
	if got := dependencyPURL(dependencyEntry{Name: "group:artifact", Version: "1.0.0", Ecosystem: "java"}); !strings.HasPrefix(got, "pkg:maven/") {
		t.Fatalf("unexpected java purl: %q", got)
	}
}

func TestRepoStaticAnalysisHandler_InvalidArgs(t *testing.T) {
	handler := NewRepoStaticAnalysisHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestRepoStaticAnalysisHandler_NoToolchain(t *testing.T) {
	handler := NewRepoStaticAnalysisHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain error, got %#v", err)
	}
}

func TestRepoPackageHandler_InvalidArgs(t *testing.T) {
	handler := NewRepoPackageHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestRepoPackageHandler_NoToolchain(t *testing.T) {
	handler := NewRepoPackageHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain error, got %#v", err)
	}
}

func TestDetectNodePackageArtifact_NoMatch(t *testing.T) {
	if got := detectNodePackageArtifact("no match here\nstill nothing"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
