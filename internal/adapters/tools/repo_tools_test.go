package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestDetectTestCommand(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	cmd, args, err := detectTestCommand(root, "", nil)
	if err != nil {
		t.Fatalf("unexpected detect error: %v", err)
	}
	if cmd != "go" || len(args) < 2 || args[0] != "test" {
		t.Fatalf("unexpected go command: %s %v", cmd, args)
	}

	rootPy := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPy, "pyproject.toml"), []byte("[project]\nname='x'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject failed: %v", err)
	}
	cmd, args, err = detectTestCommand(rootPy, "tests/unit", []string{"-k", "abc"})
	if err != nil {
		t.Fatalf("unexpected python detect error: %v", err)
	}
	if cmd != "pytest" || args[0] != "-q" {
		t.Fatalf("unexpected pytest command: %s %v", cmd, args)
	}

	rootNpm := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootNpm, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write package failed: %v", err)
	}
	cmd, args, err = detectTestCommand(rootNpm, "--grep demo", nil)
	if err != nil {
		t.Fatalf("unexpected npm detect error: %v", err)
	}
	if cmd != "npm" || args[0] != "test" {
		t.Fatalf("unexpected npm command: %s %v", cmd, args)
	}

	_, _, err = detectTestCommand(t.TempDir(), "", nil)
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestRepoRunTestsInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte("package main\nimport \"testing\"\nfunc TestOK(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}

	handler := &RepoRunTestsHandler{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":"./..."}`))
	if err != nil {
		t.Fatalf("unexpected run_tests error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected successful exit code, got %d", result.ExitCode)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("expected test output artifact")
	}
}

func TestRepoRunTestsValidation(t *testing.T) {
	handler := &RepoRunTestsHandler{}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"target":`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution failed when no runner exists, got %#v", err)
	}
}

func TestRepoDetectProjectTypeInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	handler := &RepoDetectProjectTypeHandler{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected detect_project_type error: %v", err)
	}

	output := result.Output.(map[string]any)
	if output["project_type"] != "go" {
		t.Fatalf("expected go project type, got %#v", output)
	}
}

func TestRepoBuildInvoke_GoModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go failed: %v", err)
	}

	handler := &RepoBuildHandler{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected repo.build error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected successful build exit code, got %d", result.ExitCode)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("expected build output artifact")
	}
}

func TestSanitizeArgs(t *testing.T) {
	result := sanitizeArgs([]string{" --ok ", "", "\x00bad"})
	if len(result) != 1 || result[0] != "--ok" {
		t.Fatalf("unexpected sanitized args: %#v", result)
	}
}

func TestDetectBuildCommand_RustAndC(t *testing.T) {
	rootRust := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootRust, "Cargo.toml"), []byte("[package]\nname='demo'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml failed: %v", err)
	}
	cmd, args, err := detectBuildCommand(rootRust, "", nil)
	if err != nil {
		t.Fatalf("unexpected rust detect error: %v", err)
	}
	if cmd != "cargo" || len(args) == 0 || args[0] != "build" {
		t.Fatalf("unexpected rust build command: %s %v", cmd, args)
	}

	rootC := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootC, "main.c"), []byte("int main(){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}
	cmd, args, err = detectBuildCommand(rootC, "", nil)
	if err != nil {
		t.Fatalf("unexpected c detect error: %v", err)
	}
	if cmd != "cc" || len(args) < 2 {
		t.Fatalf("unexpected c build command: %s %v", cmd, args)
	}
}

func TestDetectProjectTypeFromWorkspace_Extended(t *testing.T) {
	rootTS := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootTS, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootTS, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tsconfig.json failed: %v", err)
	}
	detected, ok := detectProjectTypeFromWorkspace(rootTS)
	if !ok {
		t.Fatal("expected typescript project type")
	}
	if detected.Name != "node" || detected.Flavor != "typescript" {
		t.Fatalf("unexpected typescript detection: %#v", detected)
	}

	rootRust := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootRust, "Cargo.toml"), []byte("[package]\nname='demo'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml failed: %v", err)
	}
	detected, ok = detectProjectTypeFromWorkspace(rootRust)
	if !ok || detected.Name != "rust" {
		t.Fatalf("unexpected rust detection: %#v, ok=%v", detected, ok)
	}
}

func TestFilterRepoExtraArgs_GoAllowAndDeny(t *testing.T) {
	detected := projectType{Name: "go"}

	args, err := filterRepoExtraArgs(detected, []string{"-v", "-run=TestTodo", "-count", "1"}, "test")
	if err != nil {
		t.Fatalf("expected allowed args, got error: %v", err)
	}
	if len(args) != 4 {
		t.Fatalf("unexpected filtered args: %#v", args)
	}

	_, err = filterRepoExtraArgs(detected, []string{"-exec=cat"}, "test")
	if err == nil {
		t.Fatal("expected denied args error")
	}
}

func TestRepoBuildInvoke_DeniesDisallowedExtraArgs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/repo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go failed: %v", err)
	}

	handler := &RepoBuildHandler{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"extra_args":["-exec=cat"]}`))
	if err == nil {
		t.Fatal("expected invalid argument error")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRepoRunTestsInvoke_AllowsPythonKFlagPair(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='demo'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "test_sample.py"), []byte("def test_ok():\n    assert True\n"), 0o644); err != nil {
		t.Fatalf("write python test failed: %v", err)
	}

	handler := &RepoRunTestsHandler{}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"extra_args":["-k","ok"]}`))
	if err != nil && err.Code == app.ErrorCodeInvalidArgument {
		t.Fatalf("expected python -k arg pair to be accepted, got: %#v", err)
	}
}

func TestRepoHandlers_ConstructorsAndNames(t *testing.T) {
	var runner app.CommandRunner
	if NewRepoDetectProjectTypeHandler(runner).Name() != "repo.detect_project_type" {
		t.Fatal("unexpected repo.detect_project_type name")
	}
	if NewRepoBuildHandler(runner).Name() != "repo.build" {
		t.Fatal("unexpected repo.build name")
	}
	if NewRepoRunTestsHandler(runner).Name() != "repo.run_tests" {
		t.Fatal("unexpected repo.run_tests name")
	}
}

func TestParseProjectMarkerVariants(t *testing.T) {
	if detected := parseProjectMarker("go"); detected.Name != "go" || detected.Flavor != "" {
		t.Fatalf("unexpected parsed go marker: %#v", detected)
	}
	if detected := parseProjectMarker("java:gradle"); detected.Name != "java" || detected.Flavor != "gradle" {
		t.Fatalf("unexpected parsed java marker: %#v", detected)
	}
	if detected := parseProjectMarker("unknown"); detected.Name != "unknown" {
		t.Fatalf("unexpected parsed unknown marker: %#v", detected)
	}
	if detected := parseProjectMarker("weird:marker"); detected.Name != "unknown" {
		t.Fatalf("unexpected parsed unsupported marker: %#v", detected)
	}
}

func TestBuildAndTestCommandsAcrossToolchains(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.c"), []byte("int main(void){return 0;}"), 0o644); err != nil {
		t.Fatalf("write main.c failed: %v", err)
	}

	buildCases := []struct {
		name     string
		detected projectType
		target   string
		command  string
	}{
		{name: "go", detected: projectType{Name: "go"}, target: "./...", command: "go"},
		{name: "rust", detected: projectType{Name: "rust"}, target: "", command: "cargo"},
		{name: "node", detected: projectType{Name: "node"}, target: "", command: "npm"},
		{name: "python", detected: projectType{Name: "python"}, target: "", command: "python"},
		{name: "java-gradle", detected: projectType{Name: "java", Flavor: "gradle"}, target: "", command: "gradle"},
		{name: "java-maven", detected: projectType{Name: "java", Flavor: "maven"}, target: "", command: "mvn"},
		{name: "c", detected: projectType{Name: "c"}, target: "main.c", command: "cc"},
	}
	for _, tc := range buildCases {
		command, args, err := buildCommandForProject(workspace, tc.detected, tc.target, nil)
		if err != nil {
			t.Fatalf("%s: buildCommandForProject failed: %v", tc.name, err)
		}
		if command != tc.command || len(args) == 0 {
			t.Fatalf("%s: unexpected command=%q args=%#v", tc.name, command, args)
		}
	}

	testCases := []struct {
		name     string
		detected projectType
		target   string
		command  string
	}{
		{name: "go", detected: projectType{Name: "go"}, target: "./...", command: "go"},
		{name: "rust", detected: projectType{Name: "rust"}, target: "", command: "cargo"},
		{name: "node", detected: projectType{Name: "node"}, target: "", command: "npm"},
		{name: "python", detected: projectType{Name: "python"}, target: "", command: "pytest"},
		{name: "java-gradle", detected: projectType{Name: "java", Flavor: "gradle"}, target: "", command: "gradle"},
		{name: "java-maven", detected: projectType{Name: "java", Flavor: "maven"}, target: "", command: "mvn"},
		{name: "c", detected: projectType{Name: "c"}, target: "main.c", command: "cc"},
	}
	for _, tc := range testCases {
		command, args, err := testCommandForProject(workspace, tc.detected, tc.target, nil)
		if err != nil {
			t.Fatalf("%s: testCommandForProject failed: %v", tc.name, err)
		}
		if command != tc.command || len(args) == 0 {
			t.Fatalf("%s: unexpected command=%q args=%#v", tc.name, command, args)
		}
	}
}

func TestRepoExtraArgAllowlists(t *testing.T) {
	if expects, ok := allowRustExtraArg("--features"); !ok || !expects {
		t.Fatalf("expected rust --features to be allowed and require value, got ok=%v expects=%v", ok, expects)
	}
	if _, ok := allowRustExtraArg("-Zunstable-options"); ok {
		t.Fatal("expected rust -Z* to be rejected")
	}

	if expects, ok := allowJavaExtraArg("maven", "build", "-P"); !ok || !expects {
		t.Fatalf("expected maven -P to be allowed and require value, got ok=%v expects=%v", ok, expects)
	}
	if _, ok := allowJavaExtraArg("maven", "build", "-Dcustom=1"); ok {
		t.Fatal("expected disallowed maven -D property")
	}
	if expects, ok := allowJavaExtraArg("gradle", "test", "--tests"); !ok || !expects {
		t.Fatalf("expected gradle --tests to be allowed and require value, got ok=%v expects=%v", ok, expects)
	}

	if _, ok := allowRepoExtraArg(projectType{Name: "node"}, "test", "--watch"); ok {
		t.Fatal("expected node extra args to be denied")
	}
	if hasDangerousArgSyntax("safe;rm -rf /") == false {
		t.Fatal("expected dangerous arg syntax to be detected")
	}
}
