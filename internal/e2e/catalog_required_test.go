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

// TestToolMatrixE2EIsRegistered guards the data-driven per-tool E2E suite: the
// registry must list the matrix test, its assets must exist, and the unified
// runner image must ship both the matrix runner and the catalog that drives it.
func TestToolMatrixE2EIsRegistered(t *testing.T) {
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

	entry, ok := entries["00"]
	if !ok {
		t.Fatalf("per-tool matrix E2E (id 00) is missing from %s", catalogPath)
	}
	if entry.Name != "00-tool-matrix" {
		t.Fatalf("E2E test 00: expected name %q, got %q", "00-tool-matrix", entry.Name)
	}
	if entry.JobName != "e2e-tool-matrix" {
		t.Fatalf("E2E test 00: expected job_name %q, got %q", "e2e-tool-matrix", entry.JobName)
	}

	testDir := filepath.Join(root, "e2e", "tests", "00-tool-matrix")
	for _, rel := range []string{"Dockerfile", "job.yaml", "test_tool_matrix.py"} {
		path := filepath.Join(testDir, rel)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("required E2E asset missing: %s (%v)", path, statErr)
		}
	}

	dockerfilePath := filepath.Join(root, "e2e", "Dockerfile")
	rawDockerfile, dockerReadErr := os.ReadFile(dockerfilePath)
	if dockerReadErr != nil {
		t.Fatalf("read %s: %v", dockerfilePath, dockerReadErr)
	}
	dockerfile := string(rawDockerfile)

	requiredCopies := []string{
		"COPY e2e/tests/00-tool-matrix/test_tool_matrix.py",
		"COPY internal/adapters/tools/catalog_defaults.yaml",
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
