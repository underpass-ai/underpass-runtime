package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildContextDigest_GoProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/myapp\ngo 1.21\n")
	writeFile(t, dir, "Dockerfile", "FROM golang:1.21\n")
	os.MkdirAll(filepath.Join(dir, "cmd"), 0o755)

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.Version != "v1" {
		t.Fatalf("expected version v1, got %s", digest.Version)
	}
	if digest.RepoLanguage != "go" {
		t.Fatalf("expected go, got %s", digest.RepoLanguage)
	}
	if digest.ProjectType != "service" {
		t.Fatalf("expected service (has Dockerfile), got %s", digest.ProjectType)
	}
	if !digest.HasDockerfile {
		t.Fatal("expected has_dockerfile true")
	}
	if digest.TestStatus != "unknown" {
		t.Fatalf("expected unknown test status, got %s", digest.TestStatus)
	}
}

func TestBuildContextDigest_PythonLibrary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[build-system]\nrequires = [\"setuptools\"]\n")

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.RepoLanguage != "python" {
		t.Fatalf("expected python, got %s", digest.RepoLanguage)
	}
	if digest.ProjectType != "library" {
		t.Fatalf("expected library, got %s", digest.ProjectType)
	}
}

func TestBuildContextDigest_NodeProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"myapp","dependencies":{"react":"^18"}}`)

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.RepoLanguage != "javascript" {
		t.Fatalf("expected javascript, got %s", digest.RepoLanguage)
	}
	if len(digest.Frameworks) != 1 || digest.Frameworks[0] != "react" {
		t.Fatalf("expected [react], got %v", digest.Frameworks)
	}
}

func TestBuildContextDigest_K8sManifests(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "k8s"), 0o755)

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if !digest.HasK8sManifests {
		t.Fatal("expected has_k8s_manifests true for k8s/ dir")
	}
}

func TestBuildContextDigest_DeployDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "deploy"), 0o755)

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if !digest.HasK8sManifests {
		t.Fatal("expected has_k8s_manifests true for deploy/ dir")
	}
}

func TestBuildContextDigest_ManifestsDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "manifests"), 0o755)

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if !digest.HasK8sManifests {
		t.Fatal("expected has_k8s_manifests true for manifests/ dir")
	}
}

func TestBuildContextDigest_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.RepoLanguage != "unknown" {
		t.Fatalf("expected unknown language, got %s", digest.RepoLanguage)
	}
	if digest.ProjectType != "unknown" {
		t.Fatalf("expected unknown project type, got %s", digest.ProjectType)
	}
	if digest.HasDockerfile {
		t.Fatal("expected has_dockerfile false")
	}
	if digest.HasK8sManifests {
		t.Fatal("expected has_k8s_manifests false")
	}
	if len(digest.Frameworks) != 0 {
		t.Fatalf("expected empty frameworks, got %v", digest.Frameworks)
	}
	if len(digest.RecentOutcomes) != 0 {
		t.Fatalf("expected empty outcomes, got %v", digest.RecentOutcomes)
	}
	if len(digest.ActiveToolset) != 0 {
		t.Fatalf("expected empty toolset, got %v", digest.ActiveToolset)
	}
}

func TestBuildContextDigest_TestStatusPassing(t *testing.T) {
	dir := t.TempDir()
	outcomes := []OutcomeSummary{
		{ToolName: "repo.test", Status: "succeeded", ExitCode: 0},
	}

	digest := BuildContextDigest(context.Background(), dir, outcomes, nil)

	if digest.TestStatus != "passing" {
		t.Fatalf("expected passing, got %s", digest.TestStatus)
	}
}

func TestBuildContextDigest_TestStatusFailing(t *testing.T) {
	dir := t.TempDir()
	outcomes := []OutcomeSummary{
		{ToolName: "repo.test", Status: "failed", ExitCode: 1},
	}

	digest := BuildContextDigest(context.Background(), dir, outcomes, nil)

	if digest.TestStatus != "failing" {
		t.Fatalf("expected failing, got %s", digest.TestStatus)
	}
}

func TestBuildContextDigest_TestStatusNoTestTool(t *testing.T) {
	dir := t.TempDir()
	outcomes := []OutcomeSummary{
		{ToolName: "fs.read", Status: "succeeded", ExitCode: 0},
	}

	digest := BuildContextDigest(context.Background(), dir, outcomes, nil)

	if digest.TestStatus != "unknown" {
		t.Fatalf("expected unknown when no test tool, got %s", digest.TestStatus)
	}
}

func TestBuildContextDigest_SecurityPosture(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".trivyignore", "CVE-2024-1234\n")

	digest := BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.SecurityPosture != "warnings" {
		t.Fatalf("expected warnings posture, got %s", digest.SecurityPosture)
	}
}

func TestBuildContextDigest_WithToolsetAndOutcomes(t *testing.T) {
	dir := t.TempDir()
	outcomes := []OutcomeSummary{
		{ToolName: "fs.read", Status: "succeeded"},
		{ToolName: "repo.test", Status: "succeeded"},
	}
	toolset := []string{"core", "repo"}

	digest := BuildContextDigest(context.Background(), dir, outcomes, toolset)

	if len(digest.RecentOutcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(digest.RecentOutcomes))
	}
	if len(digest.ActiveToolset) != 2 {
		t.Fatalf("expected 2 toolset entries, got %d", len(digest.ActiveToolset))
	}
}

func TestDetectLanguage_AllMarkers(t *testing.T) {
	tests := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "javascript"},
		{"setup.py", "python"},
		{"requirements.txt", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
		{"mix.exs", "elixir"},
		{"composer.json", "php"},
	}

	for _, tt := range tests {
		dir := t.TempDir()
		writeFile(t, dir, tt.file, "content")
		lang := detectLanguage(dir)
		if lang != tt.lang {
			t.Fatalf("file %s: expected %s, got %s", tt.file, tt.lang, lang)
		}
	}
}

func TestDetectLanguage_CSharp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "MyProject.csproj", "<Project></Project>")
	lang := detectLanguage(dir)
	if lang != "csharp" {
		t.Fatalf("expected csharp, got %s", lang)
	}
}

func TestDetectProjectType_CLI(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cmd"), 0o755)

	pt := detectProjectType(dir)
	if pt != "cli" {
		t.Fatalf("expected cli, got %s", pt)
	}
}

func TestDetectFrameworks_GoGin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "require github.com/gin-gonic/gin v1.9.0\n")

	frameworks := detectFrameworks(dir)
	if len(frameworks) != 1 || frameworks[0] != "gin" {
		t.Fatalf("expected [gin], got %v", frameworks)
	}
}

func TestDetectFrameworks_PythonDjango(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "django==4.2\n")

	frameworks := detectFrameworks(dir)
	if len(frameworks) != 1 || frameworks[0] != "django" {
		t.Fatalf("expected [django], got %v", frameworks)
	}
}

func TestDetectFrameworks_None(t *testing.T) {
	dir := t.TempDir()
	frameworks := detectFrameworks(dir)
	if len(frameworks) != 0 {
		t.Fatalf("expected empty, got %v", frameworks)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
