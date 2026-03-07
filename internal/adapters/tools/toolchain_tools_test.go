package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestRepoDetectToolchainInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewRepoDetectToolchainHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected detect toolchain error: %v", err)
	}

	output := result.Output.(map[string]any)
	if output["language"] != "go" {
		t.Fatalf("expected go language, got %#v", output)
	}
	if output["build_system"] != "go-mod" {
		t.Fatalf("expected go-mod build system, got %#v", output)
	}
}

func TestRepoValidateInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewRepoValidateHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":"./..."}`))
	if err != nil {
		t.Fatalf("unexpected repo.validate error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected repo.validate exit code 0, got %d", result.ExitCode)
	}
}

func TestRepoTestAliasInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewRepoTestHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":"./..."}`))
	if err != nil {
		t.Fatalf("unexpected repo.test error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected successful exit code, got %d", result.ExitCode)
	}
}

func TestGoBuildInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewGoBuildHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"target":"./...","output_name":"appbin","ldflags":"-s -w"}`),
	)
	if err != nil {
		t.Fatalf("unexpected go.build error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected go.build exit code 0, got %d", result.ExitCode)
	}

	output := result.Output.(map[string]any)
	if output["compiled_binary_path"] != "appbin" {
		t.Fatalf("unexpected compiled_binary_path: %#v", output)
	}
	if _, statErr := os.Stat(filepath.Join(root, "appbin")); statErr != nil {
		t.Fatalf("expected compiled binary to exist: %v", statErr)
	}
}

func TestGoBuildRejectsUnsupportedLdflags(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewGoBuildHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"target":"./...","ldflags":"-X main.Version=1"}`),
	)
	if err == nil {
		t.Fatal("expected invalid argument error for unsupported ldflags")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument code, got %#v", err)
	}
}

func TestGoTestInvoke_WithCoverage(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewGoTestHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"package":"./...","coverage":true}`))
	if err != nil {
		t.Fatalf("unexpected go.test error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected go.test exit code 0, got %d", result.ExitCode)
	}

	output := result.Output.(map[string]any)
	if output["coverage_percent"] == nil {
		t.Fatalf("expected non-nil coverage_percent, got %#v", output)
	}
}

func TestGoModTidyInvoke(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewGoModTidyHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected go.mod.tidy error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected go.mod.tidy exit code 0, got %d", result.ExitCode)
	}
}

func TestGoGenerateInvoke(t *testing.T) {
	root := t.TempDir()
	writeGoModuleFixture(t, root)

	handler := NewGoGenerateHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":"./..."}`))
	if err != nil {
		t.Fatalf("unexpected go.generate error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected go.generate exit code 0, got %d", result.ExitCode)
	}
}

func TestPythonInstallDepsRejectsUseVenvFalse(t *testing.T) {
	handler := NewPythonInstallDepsHandler(nil)
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"use_venv":false}`))
	if err == nil {
		t.Fatal("expected invalid argument for use_venv=false")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument code, got %#v", err)
	}
}

func TestCBuildRejectsInvalidStandard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.c"), []byte("int main(){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}

	handler := NewCBuildHandler(nil)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"standard":"gnu17"}`))
	if err == nil {
		t.Fatal("expected invalid argument for unsupported standard")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument code, got %#v", err)
	}
}

func TestMapProjectTypeToToolchain_Extended(t *testing.T) {
	rust := mapProjectTypeToToolchain(projectType{Name: "rust", Flavor: "cargo"})
	if rust.Language != "rust" || rust.BuildSystem != "cargo" {
		t.Fatalf("unexpected rust toolchain mapping: %#v", rust)
	}

	typescript := mapProjectTypeToToolchain(projectType{Name: "node", Flavor: "typescript"})
	if typescript.Language != "node" || typescript.BuildSystem != "npm-ts" {
		t.Fatalf("unexpected typescript mapping: %#v", typescript)
	}

	cLang := mapProjectTypeToToolchain(projectType{Name: "c", Flavor: "cc"})
	if cLang.Language != "c" || cLang.BuildSystem != "cc" {
		t.Fatalf("unexpected c mapping: %#v", cLang)
	}
}

func TestToolchainHandlerNames(t *testing.T) {
	if NewRepoDetectToolchainHandler(nil).Name() != "repo.detect_toolchain" {
		t.Fatal("unexpected repo.detect_toolchain name")
	}
	if NewRepoValidateHandler(nil).Name() != "repo.validate" {
		t.Fatal("unexpected repo.validate name")
	}
	if NewRepoTestHandler(nil).Name() != "repo.test" {
		t.Fatal("unexpected repo.test name")
	}
	if NewGoModTidyHandler(nil).Name() != "go.mod.tidy" {
		t.Fatal("unexpected go.mod.tidy name")
	}
	if NewGoGenerateHandler(nil).Name() != "go.generate" {
		t.Fatal("unexpected go.generate name")
	}
	if NewGoBuildHandler(nil).Name() != "go.build" {
		t.Fatal("unexpected go.build name")
	}
	if NewGoTestHandler(nil).Name() != "go.test" {
		t.Fatal("unexpected go.test name")
	}
}

func TestValidateCommandForProject_AllToolchains(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.c"), []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}

	cases := []struct {
		name     string
		detected projectType
		target   string
		command  string
	}{
		{name: "go", detected: projectType{Name: "go"}, target: "./...", command: "go"},
		{name: "rust", detected: projectType{Name: "rust"}, target: "", command: "cargo"},
		{name: "node", detected: projectType{Name: "node"}, target: "", command: "npm"},
		{name: "python", detected: projectType{Name: "python"}, target: "", command: "python"},
		{name: "java-maven", detected: projectType{Name: "java", Flavor: "maven"}, target: "", command: "mvn"},
		{name: "java-gradle", detected: projectType{Name: "java", Flavor: "gradle"}, target: "", command: "gradle"},
		{name: "c", detected: projectType{Name: "c"}, target: "main.c", command: "cc"},
	}
	for _, tc := range cases {
		command, args, err := validateCommandForProject(workspace, tc.detected, tc.target)
		if err != nil {
			t.Fatalf("%s: unexpected validateCommandForProject error: %v", tc.name, err)
		}
		if command != tc.command || len(args) == 0 {
			t.Fatalf("%s: unexpected command=%q args=%#v", tc.name, command, args)
		}
	}

	if _, _, err := validateCommandForProject(workspace, projectType{Name: "unknown"}, ""); err == nil {
		t.Fatal("expected validateCommandForProject error for unsupported project")
	}
}

func TestToolchainHelpers_SanitizeOutputName(t *testing.T) {
	if value, err := sanitizeOutputName("artifact.bin"); err != nil || value != "artifact.bin" {
		t.Fatalf("unexpected sanitizeOutputName valid result: value=%q err=%v", value, err)
	}
	if _, err := sanitizeOutputName("../artifact.bin"); err == nil {
		t.Fatal("expected sanitizeOutputName traversal error")
	}
	if _, err := sanitizeOutputName(strings.Repeat("a", 130)); err == nil {
		t.Fatal("expected sanitizeOutputName length error")
	}
}

func writeGoModuleFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc add(a, b int) int { return a + b }\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package main\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if add(1,2)!=3 { t.Fatal(\"bad\") } }\n"), 0o644); err != nil {
		t.Fatalf("write main_test.go failed: %v", err)
	}
}
