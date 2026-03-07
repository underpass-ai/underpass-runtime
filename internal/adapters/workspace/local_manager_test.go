package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestLocalManager_CreateSessionFromSourcePath(t *testing.T) {
	ctx := context.Background()
	manager := NewLocalManager(t.TempDir())

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write source file failed: %v", err)
	}

	session, err := manager.CreateSession(ctx, app.CreateSessionRequest{
		SessionID:       "session-1",
		SourceRepoPath:  source,
		Principal:       domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		ExpiresInSecond: 60,
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	if session.ID != "session-1" {
		t.Fatalf("unexpected session id: %s", session.ID)
	}
	if _, err := os.Stat(filepath.Join(session.WorkspacePath, "README.md")); err != nil {
		t.Fatalf("expected copied file in workspace: %v", err)
	}

	loaded, found, err := manager.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if loaded.Principal.ActorID != "alice" {
		t.Fatalf("unexpected principal: %+v", loaded.Principal)
	}
}

func TestLocalManager_CloseSessionRemovesWorkspace(t *testing.T) {
	ctx := context.Background()
	manager := NewLocalManager(t.TempDir())

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write source failed: %v", err)
	}

	session, err := manager.CreateSession(ctx, app.CreateSessionRequest{
		SessionID:       "session-close",
		SourceRepoPath:  source,
		Principal:       domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		ExpiresInSecond: 60,
	})
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	workspaceRoot := filepath.Dir(session.WorkspacePath)
	if err := manager.CloseSession(ctx, session.ID); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if _, err := os.Stat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("expected workspace root removed, got err=%v", err)
	}

	if err := manager.CloseSession(ctx, "missing"); err != nil {
		t.Fatalf("expected closing missing session to be no-op: %v", err)
	}
}

func TestLocalManager_ExpiredSessionGetsEvicted(t *testing.T) {
	ctx := context.Background()
	manager := NewLocalManager(t.TempDir())

	session, err := manager.CreateSession(ctx, app.CreateSessionRequest{
		SessionID:       "session-expired",
		Principal:       domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		ExpiresInSecond: 1,
	})
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	manager.mu.Lock()
	record := manager.session[session.ID]
	record.ExpiresAt = time.Now().UTC().Add(-1 * time.Minute)
	manager.session[session.ID] = record
	manager.mu.Unlock()

	_, found, err := manager.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if found {
		t.Fatal("expected expired session to be evicted")
	}
}

func TestCloneRepoFailure(t *testing.T) {
	ctx := context.Background()
	err := cloneRepo(ctx, "https://invalid.localhost/not-found.git", "", filepath.Join(t.TempDir(), "repo"))
	if err == nil {
		t.Fatal("expected clone error for invalid URL")
	}
}

func TestCopyDirectoryNonExistent(t *testing.T) {
	err := copyDirectory(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil {
		t.Fatal("expected copy error for missing source")
	}
}
