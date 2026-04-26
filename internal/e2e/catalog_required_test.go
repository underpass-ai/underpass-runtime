package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type e2eCatalog struct {
	WorkspaceTests []e2eCatalogEntry `yaml:"workspace_tests"`
}

type e2eCatalogEntry struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	JobName string `yaml:"job_name"`
}

func TestRequiredRuntimeSpecialistE2ETestsAreRegistered(t *testing.T) {
	root := repoRoot(t)

	catalogPath := filepath.Join(root, "e2e", "tests", "e2e_tests.yaml")
	rawCatalog, readErr := os.ReadFile(catalogPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", catalogPath, readErr)
	}

	var catalog e2eCatalog
	if unmarshalErr := yaml.Unmarshal(rawCatalog, &catalog); unmarshalErr != nil {
		t.Fatalf("parse %s: %v", catalogPath, unmarshalErr)
	}

	entries := map[string]e2eCatalogEntry{}
	for _, entry := range catalog.WorkspaceTests {
		entries[entry.ID] = entry
	}

	required := map[string]struct {
		name    string
		jobName string
		script  string
	}{
		"23": {
			name:    "23-runtime-rollout-tools",
			jobName: "e2e-runtime-rollout-tools",
			script:  "test_runtime_rollout_tools.py",
		},
		"24": {
			name:    "24-runtime-saturation-notify-tools",
			jobName: "e2e-runtime-saturation-notify-tools",
			script:  "test_runtime_saturation_notify_tools.py",
		},
	}

	for id, expected := range required {
		entry, ok := entries[id]
		if !ok {
			t.Fatalf("required E2E test %s is missing from %s", id, catalogPath)
		}
		if entry.Name != expected.name {
			t.Fatalf("E2E test %s: expected name %q, got %q", id, expected.name, entry.Name)
		}
		if entry.JobName != expected.jobName {
			t.Fatalf("E2E test %s: expected job_name %q, got %q", id, expected.jobName, entry.JobName)
		}

		testDir := filepath.Join(root, "e2e", "tests", expected.name)
		for _, rel := range []string{"Dockerfile", "Makefile", "job.yaml", expected.script} {
			path := filepath.Join(testDir, rel)
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("required E2E asset missing for test %s: %s (%v)", id, path, statErr)
			}
		}
	}

	dockerfilePath := filepath.Join(root, "e2e", "Dockerfile")
	rawDockerfile, dockerReadErr := os.ReadFile(dockerfilePath)
	if dockerReadErr != nil {
		t.Fatalf("read %s: %v", dockerfilePath, dockerReadErr)
	}
	dockerfile := string(rawDockerfile)

	requiredCopies := []string{
		"COPY e2e/tests/23-runtime-rollout-tools/test_runtime_rollout_tools.py",
		"COPY e2e/tests/24-runtime-saturation-notify-tools/test_runtime_saturation_notify_tools.py",
	}
	for _, requiredCopy := range requiredCopies {
		if !strings.Contains(dockerfile, requiredCopy) {
			t.Fatalf("unified E2E runner is missing %q in %s", requiredCopy, dockerfilePath)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
