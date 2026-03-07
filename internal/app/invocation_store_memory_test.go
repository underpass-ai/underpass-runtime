package app

import (
	"context"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestInMemoryInvocationStore_SaveAndGet(t *testing.T) {
	store := NewInMemoryInvocationStore()
	invocation := domain.Invocation{
		ID:        "inv-1",
		SessionID: "session-1",
		ToolName:  "fs.read",
		Status:    domain.InvocationStatusSucceeded,
		StartedAt: time.Now().UTC(),
	}

	if err := store.Save(context.Background(), invocation); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	stored, found, err := store.Get(context.Background(), invocation.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatalf("expected invocation to be found")
	}
	if stored.ID != invocation.ID {
		t.Fatalf("unexpected invocation id: %s", stored.ID)
	}
}

func TestInMemoryInvocationStore_GetMissing(t *testing.T) {
	store := NewInMemoryInvocationStore()
	_, found, err := store.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if found {
		t.Fatalf("expected missing invocation")
	}
}
