package valkey

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// fakeRedis implements redis.Cmdable subset needed for tests.
// We use miniredis for a real in-process Redis.
// For unit tests without external deps, we test serialization logic directly.

func TestPolicyStoreKey(t *testing.T) {
	store := &PolicyStore{keyPrefix: "tool_policy"}
	want := "tool_policy:gen:go:std:fs.write"
	if got := store.key("gen:go:std", "fs.write"); got != want {
		t.Errorf("key() = %q, want %q", got, want)
	}
}

func TestPolicyRoundTripJSON(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	policy := domain.ToolPolicy{
		ContextSignature: "gen:go:std",
		ToolID:           "fs.write",
		Alpha:            91.0,
		Beta:             11.0,
		P95LatencyMs:     250,
		P95Cost:          0.5,
		ErrorRate:        0.1,
		NSamples:         100,
		FreshnessTs:      now,
		Confidence:       0.892,
	}

	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got domain.ToolPolicy
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ContextSignature != policy.ContextSignature {
		t.Errorf("ContextSignature = %q, want %q", got.ContextSignature, policy.ContextSignature)
	}
	if got.Alpha != policy.Alpha {
		t.Errorf("Alpha = %f, want %f", got.Alpha, policy.Alpha)
	}
	if got.Beta != policy.Beta {
		t.Errorf("Beta = %f, want %f", got.Beta, policy.Beta)
	}
	if got.P95LatencyMs != policy.P95LatencyMs {
		t.Errorf("P95LatencyMs = %d, want %d", got.P95LatencyMs, policy.P95LatencyMs)
	}
	if got.NSamples != policy.NSamples {
		t.Errorf("NSamples = %d, want %d", got.NSamples, policy.NSamples)
	}
}

func TestPolicyStoreWithMiniredis(t *testing.T) {
	srv := startMiniredis(t)
	store, err := NewPolicyStoreFromAddress(context.Background(), srv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil)
	if err != nil {
		t.Fatalf("NewPolicyStoreFromAddress: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

	// Write single policy
	policy := domain.ToolPolicy{
		ContextSignature: "gen:go:std",
		ToolID:           "fs.write",
		Alpha:            91, Beta: 11,
		P95LatencyMs: 250, P95Cost: 0.5,
		ErrorRate: 0.1, NSamples: 100,
		FreshnessTs: now, Confidence: 0.892,
	}

	err = store.WritePolicy(ctx, policy)
	if err != nil {
		t.Fatalf("WritePolicy: %v", err)
	}

	// Read back
	got, found, err := store.ReadPolicy(ctx, "gen:go:std", "fs.write")
	if err != nil {
		t.Fatalf("ReadPolicy: %v", err)
	}
	if !found {
		t.Fatal("policy not found")
	}
	if got.Alpha != 91 || got.Beta != 11 {
		t.Errorf("Alpha=%f Beta=%f, want 91/11", got.Alpha, got.Beta)
	}

	// Read non-existent
	_, found, err = store.ReadPolicy(ctx, "gen:go:std", "nonexistent")
	if err != nil {
		t.Fatalf("ReadPolicy nonexistent: %v", err)
	}
	if found {
		t.Error("expected not found")
	}
}

func TestNewPolicyStoreFromAddressFailure(t *testing.T) {
	_, err := NewPolicyStoreFromAddress(context.Background(), "localhost:1", "", 0, "tp", time.Minute, nil)
	if err == nil {
		t.Fatal("expected error from invalid address")
	}
}

func TestPolicyStoreWriteBatch(t *testing.T) {
	srv := startMiniredis(t)
	store, err := NewPolicyStoreFromAddress(context.Background(), srv.Addr(), "", 0, "tool_policy", 10*time.Minute, nil)
	if err != nil {
		t.Fatalf("NewPolicyStoreFromAddress: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()

	policies := []domain.ToolPolicy{
		{ContextSignature: "gen:go:std", ToolID: "fs.write", Alpha: 10, Beta: 2, FreshnessTs: now},
		{ContextSignature: "gen:go:std", ToolID: "fs.read", Alpha: 50, Beta: 1, FreshnessTs: now},
		{ContextSignature: "test:py:std", ToolID: "git.commit", Alpha: 30, Beta: 5, FreshnessTs: now},
	}

	if err := store.WritePolicies(ctx, policies); err != nil {
		t.Fatalf("WritePolicies: %v", err)
	}

	// Verify all written
	for _, p := range policies {
		got, found, err := store.ReadPolicy(ctx, p.ContextSignature, p.ToolID)
		if err != nil {
			t.Fatalf("ReadPolicy %s/%s: %v", p.ContextSignature, p.ToolID, err)
		}
		if !found {
			t.Errorf("policy %s/%s not found", p.ContextSignature, p.ToolID)
		}
		if got.Alpha != p.Alpha {
			t.Errorf("policy %s/%s Alpha=%f, want %f", p.ContextSignature, p.ToolID, got.Alpha, p.Alpha)
		}
	}
}
