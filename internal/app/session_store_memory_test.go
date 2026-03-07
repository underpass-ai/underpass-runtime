package app

import (
	"context"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestInMemorySessionStore_SaveGetDelete(t *testing.T) {
	store := NewInMemorySessionStore()
	session := domain.Session{
		ID:        "session-1",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}

	if err := store.Save(context.Background(), session); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, found, err := store.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if loaded.ID != session.ID {
		t.Fatalf("unexpected session id: %s", loaded.ID)
	}

	if err := store.Delete(context.Background(), "session-1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	_, found, err = store.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("get after delete failed: %v", err)
	}
	if found {
		t.Fatal("expected deleted session to be missing")
	}
}

func TestInMemorySessionStore_ExpiresSession(t *testing.T) {
	store := NewInMemorySessionStore()
	session := domain.Session{
		ID:        "session-expired",
		CreatedAt: time.Now().UTC().Add(-2 * time.Minute),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	if err := store.Save(context.Background(), session); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	_, found, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if found {
		t.Fatal("expected expired session to be evicted")
	}
}
