package policy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/underpass-ai/underpass-runtime/internal/app"
)

func setupMini(t *testing.T) (*miniredis.Miniredis, *ValkeyPolicyReader) {
	t.Helper()
	m := miniredis.RunT(t)
	r, err := NewValkeyPolicyReader(m.Addr(), "", 0, "tool_policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return m, r
}

func seedPolicy(t *testing.T, m *miniredis.Miniredis, key string, p app.ToolPolicy) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	m.Set(key, string(data))
}

func TestReadPolicy_Found(t *testing.T) {
	m, r := setupMini(t)
	seedPolicy(t, m, "tool_policy:io:go:standard:fs.read_file", app.ToolPolicy{
		ContextSignature: "io:go:standard",
		ToolID:           "fs.read_file",
		Confidence:       0.85,
		NSamples:         100,
		FreshnessTs:      time.Now(),
	})

	p, found, err := r.ReadPolicy(context.Background(), "io:go:standard", "fs.read_file")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected policy to be found")
	}
	if p.ToolID != "fs.read_file" {
		t.Fatalf("expected fs.read_file, got %s", p.ToolID)
	}
	if p.Confidence != 0.85 {
		t.Fatalf("expected confidence 0.85, got %f", p.Confidence)
	}
}

func TestReadPolicy_NotFound(t *testing.T) {
	_, r := setupMini(t)
	_, found, err := r.ReadPolicy(context.Background(), "io:go:standard", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestReadPoliciesForContext(t *testing.T) {
	m, r := setupMini(t)
	seedPolicy(t, m, "tool_policy:io:go:standard:fs.read_file", app.ToolPolicy{
		ToolID: "fs.read_file", Confidence: 0.85, NSamples: 50,
	})
	seedPolicy(t, m, "tool_policy:io:go:standard:fs.write_file", app.ToolPolicy{
		ToolID: "fs.write_file", Confidence: 0.70, NSamples: 30,
	})
	seedPolicy(t, m, "tool_policy:vcs:go:standard:git.commit", app.ToolPolicy{
		ToolID: "git.commit", Confidence: 0.60, NSamples: 20,
	})

	policies, err := r.ReadPoliciesForContext(context.Background(), "io:go:standard")
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies for io:go:standard, got %d", len(policies))
	}
	if policies["fs.read_file"].Confidence != 0.85 {
		t.Fatalf("expected 0.85, got %f", policies["fs.read_file"].Confidence)
	}
}

func TestReadPoliciesForContext_Empty(t *testing.T) {
	_, r := setupMini(t)
	policies, err := r.ReadPoliciesForContext(context.Background(), "nonexistent:ctx")
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("expected 0 policies, got %d", len(policies))
	}
}

func TestNewValkeyPolicyReader_DefaultPrefix(t *testing.T) {
	m := miniredis.RunT(t)
	r, err := NewValkeyPolicyReader(m.Addr(), "", 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.prefix != "tool_policy" {
		t.Fatalf("expected default prefix tool_policy, got %s", r.prefix)
	}
}

func TestReadPolicy_InvalidJSON(t *testing.T) {
	m, r := setupMini(t)
	m.Set("tool_policy:io:go:standard:bad", "not json")

	_, _, err := r.ReadPolicy(context.Background(), "io:go:standard", "bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
