package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestSecurityScanDependenciesHandler_Go(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "go" {
				t.Fatalf("expected go command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output: "example.com/demo\n" +
					"github.com/google/uuid v1.6.0\n" +
					"golang.org/x/text v0.22.0\n",
			}, nil
		},
	}
	handler := NewSecurityScanDependenciesHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":".","max_dependencies":2}`))
	if err != nil {
		t.Fatalf("unexpected security.scan_dependencies error: %#v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["dependencies_count"] != 2 {
		t.Fatalf("unexpected dependencies_count: %#v", output["dependencies_count"])
	}
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", output["truncated"])
	}
}

func TestSBOMGenerateHandler_GeneratesCycloneDXArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "go" {
				t.Fatalf("expected go command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output: "example.com/demo\n" +
					"github.com/google/uuid v1.6.0\n",
			}, nil
		},
	}
	handler := NewSBOMGenerateHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":".","format":"cyclonedx-json","max_components":50}`))
	if err != nil {
		t.Fatalf("unexpected sbom.generate error: %#v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["artifact_name"] != "sbom.cdx.json" {
		t.Fatalf("unexpected artifact_name: %#v", output["artifact_name"])
	}

	foundSBOM := false
	for _, artifact := range result.Artifacts {
		if artifact.Name != "sbom.cdx.json" {
			continue
		}
		foundSBOM = true
		if !strings.Contains(string(artifact.Data), `"bomFormat": "CycloneDX"`) {
			t.Fatalf("expected CycloneDX content, got: %s", string(artifact.Data))
		}
	}
	if !foundSBOM {
		t.Fatal("expected sbom.cdx.json artifact")
	}
}

func TestSBOMGenerateHandler_RejectsUnsupportedFormat(t *testing.T) {
	handler := NewSBOMGenerateHandler(&fakeSWERuntimeCommandRunner{})
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"format":"spdx-json"}`))
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestSecurityScanDependenciesHandler_InvalidPath(t *testing.T) {
	handler := NewSecurityScanDependenciesHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"path":"../outside"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid path error, got %#v", err)
	}
}

func TestSBOMGenerateHandler_RejectsInvalidPath(t *testing.T) {
	handler := NewSBOMGenerateHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"path":"../outside","format":"cyclonedx-json"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid path error, got %#v", err)
	}
}

func TestSBOMGenerateHandler_InventoryParseFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "{invalid-json"}, nil
		},
	}
	_, err := NewSBOMGenerateHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{"path":".","format":"cyclonedx-json"}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected sbom inventory parse failure, got %#v", err)
	}
}

func TestSecurityScanDependenciesHandler_ParseFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "npm" {
				t.Fatalf("expected npm inventory command, got %q", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: "{invalid-json"}, nil
		},
	}
	_, err := NewSecurityScanDependenciesHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{"path":"."}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected dependency inventory parse failure, got %#v", err)
	}
}

func TestRuntimeDependencyParsers(t *testing.T) {
	pythonOutput := `[
  {"name":"requests","version":"2.31.0"},
  {"name":"requests","version":"2.31.0"},
  {"name":"urllib3","version":"2.2.0"},
  {"name":"click"}
]`
	pythonDeps, pythonTruncated, err := parsePythonDependencyInventory(pythonOutput, 2)
	if err != nil {
		t.Fatalf("parsePythonDependencyInventory failed: %v", err)
	}
	if len(pythonDeps) != 2 || !pythonTruncated {
		t.Fatalf("unexpected python deps result: len=%d truncated=%v", len(pythonDeps), pythonTruncated)
	}

	rustOutput := "serde v1.0.0\n├── regex v1.10.3\n└── serde v1.0.0\n"
	rustDeps, rustTruncated, err := parseRustDependencyInventory(rustOutput, 2)
	if err != nil {
		t.Fatalf("parseRustDependencyInventory failed: %v", err)
	}
	if len(rustDeps) != 2 || rustTruncated {
		t.Fatalf("unexpected rust deps result: len=%d truncated=%v", len(rustDeps), rustTruncated)
	}

	mavenOutput := "[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n[INFO] junk line\n"
	mavenDeps, mavenTruncated, err := parseMavenDependencyInventory(mavenOutput, 10)
	if err != nil {
		t.Fatalf("parseMavenDependencyInventory failed: %v", err)
	}
	if len(mavenDeps) != 1 || mavenTruncated {
		t.Fatalf("unexpected maven deps: %#v truncated=%v", mavenDeps, mavenTruncated)
	}

	gradleOutput := "+--- org.slf4j:slf4j-api:1.7.36\n\\--- org.jetbrains.kotlin:kotlin-stdlib:1.9.0 -> 1.9.22\n"
	gradleDeps, gradleTruncated, err := parseGradleDependencyInventory(gradleOutput, 10)
	if err != nil {
		t.Fatalf("parseGradleDependencyInventory failed: %v", err)
	}
	if len(gradleDeps) != 2 || gradleTruncated {
		t.Fatalf("unexpected gradle deps: %#v truncated=%v", gradleDeps, gradleTruncated)
	}
}

func TestSecurityScanDependenciesHandler_InvalidArgs(t *testing.T) {
	handler := NewSecurityScanDependenciesHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestSecurityScanDependenciesHandler_NoToolchain(t *testing.T) {
	handler := NewSecurityScanDependenciesHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"path":"."}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain error, got %#v", err)
	}
}

func TestSBOMGenerateHandler_InvalidArgs(t *testing.T) {
	handler := NewSBOMGenerateHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestSBOMGenerateHandler_NoToolchain(t *testing.T) {
	root := t.TempDir()
	handler := NewSBOMGenerateHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: root}, json.RawMessage(`{"path":".","format":"cyclonedx-json"}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain error, got %#v", err)
	}
}

func TestDependencyPURL_AllEcosystems(t *testing.T) {
	cases := []struct {
		entry  dependencyEntry
		prefix string
	}{
		{dependencyEntry{Name: "mod", Version: "1.0", Ecosystem: "go"}, "pkg:golang/"},
		{dependencyEntry{Name: "pkg", Version: "2.0", Ecosystem: "node"}, "pkg:npm/"},
		{dependencyEntry{Name: "Pkg", Version: "3.0", Ecosystem: "python"}, "pkg:pypi/pkg@"},
		{dependencyEntry{Name: "crate", Version: "4.0", Ecosystem: "rust"}, "pkg:cargo/"},
		{dependencyEntry{Name: "group:artifact", Version: "5.0", Ecosystem: "java"}, "pkg:maven/group/artifact@"},
		{dependencyEntry{Name: "nocolon", Version: "6.0", Ecosystem: "java"}, "pkg:maven/nocolon@"},
		{dependencyEntry{Name: "x", Version: "1.0", Ecosystem: "other"}, "pkg:generic/"},
		{dependencyEntry{Name: "x", Version: "", Ecosystem: "go"}, "pkg:golang/x@unknown"},
	}
	for _, tc := range cases {
		got := dependencyPURL(tc.entry)
		if !strings.HasPrefix(got, tc.prefix) {
			t.Errorf("dependencyPURL(%v) = %q, want prefix %q", tc.entry, got, tc.prefix)
		}
	}
}

func TestCollectDependencyInventory_UnsupportedToolchain(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{}
	_, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: t.TempDir()}, projectType{Name: "unknown"}, ".", 100)
	if err == nil {
		t.Fatal("expected error for unsupported toolchain")
	}
}

func TestCollectDependencyInventory_WithSubpath(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "example.com/demo\ngithub.com/foo/bar v1.0.0\n"}, nil
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: t.TempDir()}, projectType{Name: "go"}, "subdir", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) == 0 {
		t.Fatal("expected dependencies")
	}
}

func TestParseGradleCoordinate_VersionOverride(t *testing.T) {
	// inline version override (no space around ->)
	name, version, ok := parseGradleCoordinate("+--- org.slf4j:slf4j-api:1.7.36->2.0.0")
	if !ok {
		t.Fatal("expected ok=true for version override")
	}
	if version != "2.0.0" {
		t.Fatalf("expected version 2.0.0, got %q", version)
	}
	if name != "org.slf4j:slf4j-api" {
		t.Fatalf("expected name org.slf4j:slf4j-api, got %q", name)
	}

	// space-separated -> keeps original version (fields split drops the override)
	name2, version2, ok2 := parseGradleCoordinate("+--- org.slf4j:slf4j-api:1.7.36 -> 2.0.0")
	if !ok2 {
		t.Fatal("expected ok=true for space-separated override")
	}
	if version2 != "1.7.36" {
		t.Fatalf("expected original version 1.7.36, got %q", version2)
	}
	if name2 != "org.slf4j:slf4j-api" {
		t.Fatalf("expected name org.slf4j:slf4j-api, got %q", name2)
	}

	// (*) suffix stripping
	_, version3, ok3 := parseGradleCoordinate("+--- com.google:guava:31.0(*)")
	if !ok3 {
		t.Fatal("expected ok=true for (*) suffix")
	}
	if version3 != "31.0" {
		t.Fatalf("expected version 31.0, got %q", version3)
	}

	// empty line
	_, _, ok = parseGradleCoordinate("")
	if ok {
		t.Fatal("expected ok=false for empty line")
	}

	// not enough colons
	_, _, ok = parseGradleCoordinate("no:colons")
	if ok {
		t.Fatal("expected ok=false for insufficient colons")
	}
}

func TestCloneDependencyEntries(t *testing.T) {
	if cloneDependencyEntries(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
	if cloneDependencyEntries([]dependencyEntry{}) != nil {
		t.Fatal("expected nil for empty input")
	}
	entries := []dependencyEntry{{Name: "a", Version: "1"}}
	cloned := cloneDependencyEntries(entries)
	if len(cloned) != 1 || cloned[0].Name != "a" {
		t.Fatalf("unexpected clone result: %#v", cloned)
	}
}

// ---------------------------------------------------------------------------
// collectDependencyInventory — python, rust, java/maven, java/gradle branches
// ---------------------------------------------------------------------------

func TestCollectDependencyInventory_PythonBranch(t *testing.T) {
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "python3" && spec.Command != "python" {
				t.Fatalf("expected python executable, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   `[{"name":"requests","version":"2.31.0"},{"name":"click","version":"8.1.0"}]`,
			}, nil
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "python"}, ".", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(result.Dependencies))
	}
	if result.Dependencies[0].Ecosystem != "python" {
		t.Fatalf("expected python ecosystem, got %q", result.Dependencies[0].Ecosystem)
	}
}

func TestCollectDependencyInventory_RustBranch(t *testing.T) {
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "cargo" {
				t.Fatalf("expected cargo command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   "serde v1.0.0\nregex v1.10.3\n",
			}, nil
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "rust"}, ".", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(result.Dependencies))
	}
	if result.Command[0] != "cargo" {
		t.Fatalf("expected cargo in command, got %q", result.Command[0])
	}
}

func TestCollectDependencyInventory_JavaMavenBranch(t *testing.T) {
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "mvn" {
				t.Fatalf("expected mvn command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   "[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n",
			}, nil
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "java", Flavor: "maven"}, ".", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	if result.Command[0] != "mvn" {
		t.Fatalf("expected mvn in command, got %q", result.Command[0])
	}
}

func TestCollectDependencyInventory_JavaGradleBranch(t *testing.T) {
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "gradle" {
				t.Fatalf("expected gradle command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   "+--- org.slf4j:slf4j-api:1.7.36\n",
			}, nil
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "java", Flavor: "gradle"}, ".", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	if result.Command[0] != "gradle" {
		t.Fatalf("expected gradle in command, got %q", result.Command[0])
	}
}

func TestCollectDependencyInventory_ParseErrorNoRunError(t *testing.T) {
	// When parse fails but the runner itself succeeded, collectDependencyInventory should return parseErr.
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "{bad-json"}, nil
		},
	}
	_, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "node"}, ".", 100)
	if err == nil {
		t.Fatal("expected parse error when runner succeeds but output is invalid")
	}
}

func TestCollectDependencyInventory_RunErrorWithEmptyDeps(t *testing.T) {
	// When both runErr and parseErr occur, dependencies should be nil and result returned without error.
	root := t.TempDir()
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "{bad-json"}, errors.New("command failed")
		},
	}
	result, err := collectDependencyInventory(context.Background(), runner, domain.Session{WorkspacePath: root}, projectType{Name: "node"}, ".", 100)
	if err != nil {
		t.Fatalf("expected no error when both run and parse fail, got %v", err)
	}
	if len(result.Dependencies) != 0 {
		t.Fatalf("expected zero dependencies, got %d", len(result.Dependencies))
	}
	if result.RunErr == nil {
		t.Fatal("expected RunErr to be preserved")
	}
}

// ---------------------------------------------------------------------------
// buildSBOMResult — preview truncation (>25 components)
// ---------------------------------------------------------------------------

func TestBuildSBOMResult_PreviewTruncation(t *testing.T) {
	deps := make([]dependencyEntry, 30)
	for i := range deps {
		deps[i] = dependencyEntry{
			Name:      fmt.Sprintf("pkg-%d", i),
			Version:   "1.0.0",
			Ecosystem: "go",
			License:   "MIT",
		}
	}
	inventory := dependencyInventoryResult{
		Command:      []string{"go", "list", "-m", "all"},
		ExitCode:     0,
		Output:       "some output",
		Dependencies: deps,
		Truncated:    false,
	}
	result, domErr := buildSBOMResult("go", inventory)
	if domErr != nil {
		t.Fatalf("unexpected error: %#v", domErr)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["components_count"] != 30 {
		t.Fatalf("expected 30 total components, got %v", output["components_count"])
	}
	preview, ok := output["components"].([]map[string]any)
	if !ok {
		t.Fatalf("expected components preview to be []map[string]any, got %T", output["components"])
	}
	if len(preview) != 25 {
		t.Fatalf("expected preview truncated to 25, got %d", len(preview))
	}
}

// ---------------------------------------------------------------------------
// walkNodeDependencies — truncation when maxDependencies is reached
// ---------------------------------------------------------------------------

func TestWalkNodeDependencies_Truncation(t *testing.T) {
	tree := map[string]any{
		"alpha": map[string]any{"version": "1.0.0"},
		"bravo": map[string]any{"version": "2.0.0"},
		"charlie": map[string]any{"version": "3.0.0"},
	}
	var out []dependencyEntry
	seen := map[string]struct{}{}
	truncated := false

	walkNodeDependencies(tree, 2, seen, &out, &truncated)

	if len(out) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(out))
	}
	if !truncated {
		t.Fatal("expected truncated=true when maxDependencies reached")
	}
}

// ---------------------------------------------------------------------------
// extractNodePackageNode — non-map value returns false
// ---------------------------------------------------------------------------

func TestExtractNodePackageNode_NonMapValue(t *testing.T) {
	tree := map[string]any{
		"good": map[string]any{"version": "1.0"},
		"bad":  "not-a-map",
	}

	node, ok := extractNodePackageNode(tree, "good")
	if !ok || node == nil {
		t.Fatal("expected ok=true for map value")
	}

	_, ok = extractNodePackageNode(tree, "bad")
	if ok {
		t.Fatal("expected ok=false for non-map value")
	}

	_, ok = extractNodePackageNode(tree, "missing")
	if ok {
		t.Fatal("expected ok=false for missing key")
	}
}

// ---------------------------------------------------------------------------
// appendNodeDependencyEntry — duplicate entry + empty version
// ---------------------------------------------------------------------------

func TestAppendNodeDependencyEntry_Duplicate(t *testing.T) {
	seen := map[string]struct{}{}
	var out []dependencyEntry

	node := map[string]any{"version": "1.0.0"}
	appendNodeDependencyEntry("express", node, seen, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}

	// Duplicate — should be skipped
	appendNodeDependencyEntry("express", node, seen, &out)
	if len(out) != 1 {
		t.Fatalf("expected still 1 entry after duplicate, got %d", len(out))
	}
}

func TestAppendNodeDependencyEntry_EmptyVersion(t *testing.T) {
	seen := map[string]struct{}{}
	var out []dependencyEntry

	node := map[string]any{"version": ""}
	appendNodeDependencyEntry("lodash", node, seen, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	if out[0].Version != "unknown" {
		t.Fatalf("expected version 'unknown' for empty version, got %q", out[0].Version)
	}
}

// ---------------------------------------------------------------------------
// parseRustDependencyInventory — duplicates, truncation, v-prefix
// ---------------------------------------------------------------------------

func TestParseRustDependencyInventory_DuplicatesAndTruncation(t *testing.T) {
	output := "serde v1.0.0\nserde v1.0.0\nregex v1.10.3\ntokio v1.36.0\nhyper v0.14.0\n"
	deps, truncated, err := parseRustDependencyInventory(output, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// serde appears twice but dedup should give: serde, regex, tokio = 3, then hyper is truncated
	if len(deps) != 3 {
		t.Fatalf("expected 3 deps after dedup+truncation, got %d", len(deps))
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
}

func TestParseRustDependencyInventory_VPrefixStripping(t *testing.T) {
	output := "serde v1.0.0\nregex 1.10.3\n"
	deps, _, err := parseRustDependencyInventory(output, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range deps {
		if strings.HasPrefix(d.Version, "v") {
			t.Fatalf("v-prefix not stripped for %q: version=%q", d.Name, d.Version)
		}
	}
	if deps[0].Version != "1.0.0" {
		t.Fatalf("expected 1.0.0 for serde, got %q", deps[0].Version)
	}
}

// ---------------------------------------------------------------------------
// parseMavenDependencyInventory — spaces in group, <4 parts, duplicates, truncation
// ---------------------------------------------------------------------------

func TestParseMavenDependencyInventory_SpacesInGroupSkipped(t *testing.T) {
	output := "[INFO] some description line:has:colons:but spaces in group\n" +
		"[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n"
	deps, _, err := parseMavenDependencyInventory(output, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep (spaces line skipped), got %d", len(deps))
	}
	if deps[0].Name != "org.apache.commons:commons-lang3" {
		t.Fatalf("unexpected dep name: %q", deps[0].Name)
	}
}

func TestParseMavenDependencyInventory_LessThan4PartsSkipped(t *testing.T) {
	output := "[INFO] too:few:parts\n[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n"
	deps, _, err := parseMavenDependencyInventory(output, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep (<4 parts line skipped), got %d", len(deps))
	}
}

func TestParseMavenDependencyInventory_DuplicatesAndTruncation(t *testing.T) {
	output := "[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n" +
		"[INFO] org.apache.commons:commons-lang3:jar:3.13.0:runtime\n" +
		"[INFO] com.google.guava:guava:jar:31.0:compile\n" +
		"[INFO] org.slf4j:slf4j-api:jar:1.7.36:runtime\n"
	deps, truncated, err := parseMavenDependencyInventory(output, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After dedup: commons-lang3, guava = 2 unique. slf4j truncated.
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps after dedup+truncation, got %d", len(deps))
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
}

// ---------------------------------------------------------------------------
// parseGradleDependencyInventory — duplicates + truncation
// ---------------------------------------------------------------------------

func TestParseGradleDependencyInventory_DuplicatesAndTruncation(t *testing.T) {
	output := "+--- org.slf4j:slf4j-api:1.7.36\n" +
		"+--- org.slf4j:slf4j-api:1.7.36\n" +
		"\\--- com.google.guava:guava:31.0\n" +
		"+--- org.jetbrains.kotlin:kotlin-stdlib:1.9.0\n"
	deps, truncated, err := parseGradleDependencyInventory(output, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After dedup: slf4j-api, guava = 2 unique. kotlin-stdlib truncated.
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps after dedup+truncation, got %d", len(deps))
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
}
