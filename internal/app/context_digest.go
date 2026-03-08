package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ContextDigest is a lightweight summary of workspace state used by the
// recommendation engine and LLM context injection.
type ContextDigest struct {
	Version         string           `json:"version"`
	RepoLanguage    string           `json:"repo_language"`
	ProjectType     string           `json:"project_type"`
	Frameworks      []string         `json:"frameworks"`
	HasDockerfile   bool             `json:"has_dockerfile"`
	HasK8sManifests bool             `json:"has_k8s_manifests"`
	TestStatus      string           `json:"test_status"`
	RecentOutcomes  []OutcomeSummary `json:"recent_outcomes"`
	ActiveToolset   []string         `json:"active_toolset"`
	SecurityPosture string           `json:"security_posture"`
}

// OutcomeSummary captures a tool invocation outcome.
type OutcomeSummary struct {
	ToolName string `json:"tool_name"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
}

const digestVersion = "v1"

// BuildContextDigest constructs a context digest from the workspace directory
// by detecting project markers. RecentOutcomes and ActiveToolset are provided
// by the caller from session state.
func BuildContextDigest(_ context.Context, workspaceDir string, outcomes []OutcomeSummary, toolset []string) ContextDigest {
	d := ContextDigest{
		Version:         digestVersion,
		RepoLanguage:    detectLanguage(workspaceDir),
		ProjectType:     detectProjectType(workspaceDir),
		Frameworks:      detectFrameworks(workspaceDir),
		HasDockerfile:   fileExists(workspaceDir, "Dockerfile"),
		HasK8sManifests: dirExists(workspaceDir, "k8s") || dirExists(workspaceDir, "deploy") || dirExists(workspaceDir, "manifests"),
		TestStatus:      "unknown",
		RecentOutcomes:  outcomes,
		ActiveToolset:   toolset,
		SecurityPosture: "clean",
	}
	if d.Frameworks == nil {
		d.Frameworks = []string{}
	}
	if d.RecentOutcomes == nil {
		d.RecentOutcomes = []OutcomeSummary{}
	}
	if d.ActiveToolset == nil {
		d.ActiveToolset = []string{}
	}

	// Derive test status from recent outcomes
	d.TestStatus = deriveTestStatus(outcomes)
	d.SecurityPosture = deriveSecurityPosture(workspaceDir)

	return d
}

// detectLanguage identifies the primary programming language from marker files.
func detectLanguage(dir string) string {
	markers := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "javascript"},
		{"pyproject.toml", "python"},
		{"setup.py", "python"},
		{"requirements.txt", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
		{"mix.exs", "elixir"},
		{"composer.json", "php"},
		{"*.csproj", "csharp"},
	}
	for _, m := range markers {
		if m.file == "*.csproj" {
			if matches, _ := filepath.Glob(filepath.Join(dir, m.file)); len(matches) > 0 {
				return m.lang
			}
			continue
		}
		if fileExists(dir, m.file) {
			return m.lang
		}
	}
	return "unknown"
}

// detectProjectType identifies the project type (library, service, cli).
func detectProjectType(dir string) string {
	if fileExists(dir, "Dockerfile") || fileExists(dir, "docker-compose.yml") {
		return "service"
	}
	if dirExists(dir, "cmd") {
		return "cli"
	}
	if fileExists(dir, "setup.py") || fileExists(dir, "pyproject.toml") {
		return "library"
	}
	return "unknown"
}

// detectFrameworks returns detected framework markers.
func detectFrameworks(dir string) []string {
	detectors := []struct {
		check func() bool
		name  string
	}{
		{func() bool { return fileContains(dir, "go.mod", "gin-gonic") }, "gin"},
		{func() bool { return fileContains(dir, "go.mod", "labstack/echo") }, "echo"},
		{func() bool { return fileContains(dir, "go.mod", "go-chi/chi") }, "chi"},
		{func() bool { return fileContains(dir, "go.mod", "gorilla/mux") }, "gorilla"},
		{func() bool { return fileContains(dir, "package.json", "react") }, "react"},
		{func() bool { return fileContains(dir, "package.json", "next") }, "nextjs"},
		{func() bool { return fileContains(dir, "package.json", "express") }, "express"},
		{func() bool {
			return fileContains(dir, "requirements.txt", "django") || fileContains(dir, "pyproject.toml", "django")
		}, "django"},
		{func() bool {
			return fileContains(dir, "requirements.txt", "flask") || fileContains(dir, "pyproject.toml", "flask")
		}, "flask"},
		{func() bool {
			return fileContains(dir, "requirements.txt", "fastapi") || fileContains(dir, "pyproject.toml", "fastapi")
		}, "fastapi"},
		{func() bool { return fileContains(dir, "pom.xml", "spring") }, "spring"},
	}

	var found []string
	for _, d := range detectors {
		if d.check() {
			found = append(found, d.name)
		}
	}
	return found
}

func deriveTestStatus(outcomes []OutcomeSummary) string {
	if len(outcomes) == 0 {
		return "unknown"
	}
	for _, o := range outcomes {
		if strings.Contains(o.ToolName, "test") {
			if o.Status == "succeeded" {
				return "passing"
			}
			return "failing"
		}
	}
	return "unknown"
}

func deriveSecurityPosture(dir string) string {
	if fileExists(dir, ".trivyignore") || fileExists(dir, ".snyk") {
		return "warnings"
	}
	return "clean"
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func dirExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.IsDir()
}

func fileContains(dir, name, substr string) bool {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), strings.ToLower(substr))
}
