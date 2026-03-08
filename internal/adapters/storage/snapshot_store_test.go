package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

func setupTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "lib.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSnapshotStore_CreateAndRestore(t *testing.T) {
	artifactStore := NewLocalArtifactStore(t.TempDir())
	snapStore := NewSnapshotStore(artifactStore)
	ctx := context.Background()

	wsDir := setupTestWorkspace(t)

	// Create snapshot
	ref, err := snapStore.Create(ctx, "sess-snap-1", wsDir)
	if err != nil {
		t.Fatalf("create snapshot error: %v", err)
	}
	if ref.ID == "" {
		t.Fatal("expected non-empty snapshot ID")
	}
	if ref.SessionID != "sess-snap-1" {
		t.Fatalf("expected session_id sess-snap-1, got %s", ref.SessionID)
	}
	if ref.Size <= 0 {
		t.Fatalf("expected positive size, got %d", ref.Size)
	}
	if ref.Checksum == "" {
		t.Fatal("expected non-empty checksum")
	}
	if ref.Path == "" {
		t.Fatal("expected non-empty path")
	}

	// Restore snapshot to new directory
	restoreDir := t.TempDir()
	if err := snapStore.Restore(ctx, ref, restoreDir); err != nil {
		t.Fatalf("restore snapshot error: %v", err)
	}

	// Verify restored files
	mainGo, err := os.ReadFile(filepath.Join(restoreDir, "main.go"))
	if err != nil {
		t.Fatalf("failed to read restored main.go: %v", err)
	}
	if string(mainGo) != "package main\n" {
		t.Fatalf("restored main.go content mismatch: %q", string(mainGo))
	}

	libGo, err := os.ReadFile(filepath.Join(restoreDir, "pkg", "lib.go"))
	if err != nil {
		t.Fatalf("failed to read restored pkg/lib.go: %v", err)
	}
	if string(libGo) != "package pkg\n" {
		t.Fatalf("restored lib.go content mismatch: %q", string(libGo))
	}
}

func TestSnapshotStore_CreateMissingDir(t *testing.T) {
	artifactStore := NewLocalArtifactStore(t.TempDir())
	snapStore := NewSnapshotStore(artifactStore)

	_, err := snapStore.Create(context.Background(), "sess-missing", "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestSnapshotStore_CreateNotADirectory(t *testing.T) {
	artifactStore := NewLocalArtifactStore(t.TempDir())
	snapStore := NewSnapshotStore(artifactStore)

	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := snapStore.Create(context.Background(), "sess-file", file)
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestSnapshotStore_RestoreMissingSnapshot(t *testing.T) {
	artifactStore := NewLocalArtifactStore(t.TempDir())
	snapStore := NewSnapshotStore(artifactStore)

	ref := app.SnapshotRef{Path: "/nonexistent/snapshot.tar.gz"}
	err := snapStore.Restore(context.Background(), ref, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
}

func TestSnapshotStore_EmptyWorkspace(t *testing.T) {
	artifactStore := NewLocalArtifactStore(t.TempDir())
	snapStore := NewSnapshotStore(artifactStore)
	ctx := context.Background()

	emptyDir := t.TempDir()

	ref, err := snapStore.Create(ctx, "sess-empty", emptyDir)
	if err != nil {
		t.Fatalf("create snapshot error: %v", err)
	}

	restoreDir := t.TempDir()
	if err := snapStore.Restore(ctx, ref, restoreDir); err != nil {
		t.Fatalf("restore snapshot error: %v", err)
	}

	entries, err := os.ReadDir(restoreDir)
	if err != nil {
		t.Fatalf("read restore dir error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty restored dir, got %d entries", len(entries))
	}
}
