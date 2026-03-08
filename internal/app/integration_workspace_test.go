package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// --- Use Case: Context Digest from Workspace (Go project) ---

func TestIntegration_ContextDigest_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Create Go project markers
	writeFile(t, dir, "go.mod", "module example.com/myservice\n\ngo 1.22\n\nrequire github.com/gin-gonic/gin v1.9.0\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, dir, "Dockerfile", "FROM golang:1.22\n")
	mkdirAll(t, dir, "cmd")

	outcomes := []app.OutcomeSummary{
		{ToolName: "repo.test", Status: "succeeded", ExitCode: 0},
	}
	toolset := []string{"fs.list", "repo.test"}

	digest := app.BuildContextDigest(context.Background(), dir, outcomes, toolset)

	if digest.Version != "v1" {
		t.Fatalf("expected version v1, got %s", digest.Version)
	}
	if digest.RepoLanguage != "go" {
		t.Fatalf("expected language go, got %s", digest.RepoLanguage)
	}
	if digest.ProjectType != "service" {
		t.Fatalf("expected project type service, got %s", digest.ProjectType)
	}
	if !digest.HasDockerfile {
		t.Fatal("expected has_dockerfile=true")
	}
	if digest.TestStatus != "passing" {
		t.Fatalf("expected test_status=passing, got %s", digest.TestStatus)
	}
	if len(digest.Frameworks) == 0 || digest.Frameworks[0] != "gin" {
		t.Fatalf("expected gin framework, got %v", digest.Frameworks)
	}
	if len(digest.ActiveToolset) != 2 {
		t.Fatalf("expected 2 active tools, got %d", len(digest.ActiveToolset))
	}
	if digest.SecurityPosture != "clean" {
		t.Fatalf("expected clean security posture, got %s", digest.SecurityPosture)
	}
}

// --- Use Case: Context Digest from Workspace (Python project) ---

func TestIntegration_ContextDigest_PythonProject(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "pyproject.toml", "[tool.poetry]\nname = \"myapp\"\n\n[tool.poetry.dependencies]\nfastapi = \"^0.100\"\n")
	writeFile(t, dir, ".trivyignore", "CVE-2024-1234\n")
	mkdirAll(t, dir, "k8s")

	digest := app.BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.RepoLanguage != "python" {
		t.Fatalf("expected python, got %s", digest.RepoLanguage)
	}
	if !digest.HasK8sManifests {
		t.Fatal("expected has_k8s_manifests=true")
	}
	if digest.SecurityPosture != "warnings" {
		t.Fatalf("expected warnings (trivyignore), got %s", digest.SecurityPosture)
	}
	// Nil outcomes/toolset should be initialized to empty slices
	if digest.RecentOutcomes == nil {
		t.Fatal("expected non-nil recent_outcomes")
	}
	if digest.ActiveToolset == nil {
		t.Fatal("expected non-nil active_toolset")
	}
}

// --- Use Case: Context Digest with failing tests ---

func TestIntegration_ContextDigest_FailingTests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name": "myapp", "dependencies": {"react": "^18"}}`)

	outcomes := []app.OutcomeSummary{
		{ToolName: "repo.test", Status: "failed", ExitCode: 1},
	}

	digest := app.BuildContextDigest(context.Background(), dir, outcomes, nil)

	if digest.RepoLanguage != "javascript" {
		t.Fatalf("expected javascript, got %s", digest.RepoLanguage)
	}
	if digest.TestStatus != "failing" {
		t.Fatalf("expected test_status=failing, got %s", digest.TestStatus)
	}
}

// --- Use Case: Context Digest with unknown language ---

func TestIntegration_ContextDigest_UnknownProject(t *testing.T) {
	dir := t.TempDir()

	digest := app.BuildContextDigest(context.Background(), dir, nil, nil)

	if digest.RepoLanguage != "unknown" {
		t.Fatalf("expected unknown language, got %s", digest.RepoLanguage)
	}
	if digest.ProjectType != "unknown" {
		t.Fatalf("expected unknown project type, got %s", digest.ProjectType)
	}
}

// --- Use Case: Snapshot & Rehydration ---

func TestIntegration_SnapshotAndRestore(t *testing.T) {
	artifactRoot := t.TempDir()
	artifactStore := storage.NewLocalArtifactStore(artifactRoot)
	snapStore := storage.NewSnapshotStore(artifactStore)
	ctx := context.Background()

	// Create a workspace with files
	workspaceDir := t.TempDir()
	writeFile(t, workspaceDir, "main.go", "package main\n\nfunc main() {}\n")
	mkdirAll(t, workspaceDir, "internal")
	writeFile(t, filepath.Join(workspaceDir, "internal"), "service.go", "package internal\n")
	writeFile(t, workspaceDir, "go.mod", "module example.com/test\n\ngo 1.22\n")

	// Create snapshot
	sessionID := "snap-session-001"
	ref, snapErr := snapStore.Create(ctx, sessionID, workspaceDir)
	if snapErr != nil {
		t.Fatalf("create snapshot: %v", snapErr)
	}
	if ref.ID == "" {
		t.Fatal("expected non-empty snapshot ID")
	}
	if ref.SessionID != sessionID {
		t.Fatalf("expected session_id=%s, got %s", sessionID, ref.SessionID)
	}
	if ref.Size <= 0 {
		t.Fatalf("expected positive snapshot size, got %d", ref.Size)
	}
	if ref.Checksum == "" {
		t.Fatal("expected non-empty checksum")
	}

	// Restore to a new directory
	restoreDir := t.TempDir()
	if restoreErr := snapStore.Restore(ctx, ref, restoreDir); restoreErr != nil {
		t.Fatalf("restore snapshot: %v", restoreErr)
	}

	// Verify files are restored
	assertFileExists(t, restoreDir, "main.go")
	assertFileExists(t, restoreDir, "go.mod")
	assertFileExists(t, filepath.Join(restoreDir, "internal"), "service.go")

	// Verify file contents
	mainContent := readTestFile(t, filepath.Join(restoreDir, "main.go"))
	if mainContent != "package main\n\nfunc main() {}\n" {
		t.Fatalf("main.go content mismatch: %q", mainContent)
	}
}

// --- Use Case: Snapshot with empty workspace ---

func TestIntegration_SnapshotEmptyWorkspace(t *testing.T) {
	artifactRoot := t.TempDir()
	artifactStore := storage.NewLocalArtifactStore(artifactRoot)
	snapStore := storage.NewSnapshotStore(artifactStore)
	ctx := context.Background()

	emptyDir := t.TempDir()
	ref, snapErr := snapStore.Create(ctx, "empty-session", emptyDir)
	if snapErr != nil {
		t.Fatalf("snapshot empty workspace: %v", snapErr)
	}
	if ref.Size <= 0 {
		t.Fatalf("expected positive size even for empty snapshot (tar header), got %d", ref.Size)
	}

	restoreDir := t.TempDir()
	if restoreErr := snapStore.Restore(ctx, ref, restoreDir); restoreErr != nil {
		t.Fatalf("restore empty snapshot: %v", restoreErr)
	}
}

// --- Use Case: Write files via tool invocation, snapshot, restore, read back ---

func TestIntegration_ToolInvocation_Snapshot_Restore(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Write a file via tool invocation
	_, writeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Approved: true,
		Args:     mustJSON(t, map[string]any{"path": "snapshot-test.txt", "content": "snapshot me"}),
	})
	if writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	// Create snapshot of the workspace
	artifactRoot := t.TempDir()
	artifactStore := storage.NewLocalArtifactStore(artifactRoot)
	snapStore := storage.NewSnapshotStore(artifactStore)

	ref, snapErr := snapStore.Create(ctx, session.ID, session.WorkspacePath)
	if snapErr != nil {
		t.Fatalf("snapshot: %v", snapErr)
	}

	// Restore to new location
	restoreDir := t.TempDir()
	if restoreErr := snapStore.Restore(ctx, ref, restoreDir); restoreErr != nil {
		t.Fatalf("restore: %v", restoreErr)
	}

	// Verify the file written via tool invocation is in the snapshot
	assertFileExists(t, restoreDir, "snapshot-test.txt")
	content := readTestFile(t, filepath.Join(restoreDir, "snapshot-test.txt"))
	if content != "snapshot me" {
		t.Fatalf("content mismatch after restore: %q", content)
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func mkdirAll(t *testing.T, parts ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(parts...), 0o755); err != nil {
		t.Fatalf("mkdir %v: %v", parts, err)
	}
}

func assertFileExists(t *testing.T, parts ...string) {
	t.Helper()
	path := filepath.Join(parts...)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %s", path)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
