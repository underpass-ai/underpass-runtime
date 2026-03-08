package app

import (
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestExtractRequiredArgs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["path","mode"],"properties":{"path":{"type":"string"},"mode":{"type":"string"}}}`)
	args := extractRequiredArgs(schema)
	if len(args) != 2 {
		t.Fatalf("expected 2 required args, got %d", len(args))
	}
	if args[0] != "path" || args[1] != "mode" {
		t.Fatalf("expected [path mode], got %v", args)
	}
}

func TestExtractRequiredArgs_Empty(t *testing.T) {
	args := extractRequiredArgs(nil)
	if len(args) != 0 {
		t.Fatalf("expected 0 required args for nil schema, got %d", len(args))
	}
}

func TestExtractRequiredArgs_NoRequired(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	args := extractRequiredArgs(schema)
	if len(args) != 0 {
		t.Fatalf("expected 0 required args, got %d", len(args))
	}
}

func TestExtractRequiredArgs_InvalidJSON(t *testing.T) {
	schema := json.RawMessage(`{invalid}`)
	args := extractRequiredArgs(schema)
	if len(args) != 0 {
		t.Fatalf("expected 0 required args for invalid JSON, got %d", len(args))
	}
}

func TestDeriveTags(t *testing.T) {
	cap := &domain.Capability{
		Name:  "fs.read_file",
		Scope: domain.ScopeRepo,
	}
	tags := deriveTags(cap)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(tags), tags)
	}
	if tags[0] != "fs" {
		t.Fatalf("expected first tag 'fs', got %s", tags[0])
	}
	if tags[1] != "repo" {
		t.Fatalf("expected second tag 'repo', got %s", tags[1])
	}
}

func TestDeriveTags_NoPrefix(t *testing.T) {
	cap := &domain.Capability{
		Name:  "shell",
		Scope: domain.ScopeWorkspace,
	}
	tags := deriveTags(cap)
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag for name without dot, got %d: %v", len(tags), tags)
	}
	if tags[0] != "workspace" {
		t.Fatalf("expected tag 'workspace', got %s", tags[0])
	}
}

func TestDeriveCost(t *testing.T) {
	tests := []struct {
		name     string
		cap      domain.Capability
		expected string
	}{
		{"explicit cost_hint", domain.Capability{CostHint: "free"}, "free"},
		{"cheap timeout", domain.Capability{Constraints: domain.Constraints{TimeoutSeconds: 5}}, "cheap"},
		{"medium timeout", domain.Capability{Constraints: domain.Constraints{TimeoutSeconds: 30}}, "medium"},
		{"expensive timeout", domain.Capability{Constraints: domain.Constraints{TimeoutSeconds: 120}}, "expensive"},
		{"zero timeout", domain.Capability{Constraints: domain.Constraints{TimeoutSeconds: 0}}, "cheap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := deriveCost(&tt.cap)
			if cost != tt.expected {
				t.Fatalf("expected cost %q, got %q", tt.expected, cost)
			}
		})
	}
}

func TestToCompactTool(t *testing.T) {
	cap := &domain.Capability{
		Name:             "git.status",
		Description:      "Show working tree status",
		InputSchema:      json.RawMessage(`{"type":"object","required":["ref"]}`),
		Scope:            domain.ScopeRepo,
		SideEffects:      domain.SideEffectsNone,
		RiskLevel:        domain.RiskLow,
		RequiresApproval: false,
		Constraints:      domain.Constraints{TimeoutSeconds: 10},
	}

	compact := toCompactTool(cap)
	if compact.Name != "git.status" {
		t.Fatalf("expected name git.status, got %s", compact.Name)
	}
	if compact.Risk != "low" {
		t.Fatalf("expected risk low, got %s", compact.Risk)
	}
	if compact.SideEffects != "none" {
		t.Fatalf("expected side_effects none, got %s", compact.SideEffects)
	}
	if compact.Approval {
		t.Fatal("expected approval false")
	}
	if len(compact.RequiredArgs) != 1 || compact.RequiredArgs[0] != "ref" {
		t.Fatalf("expected required_args [ref], got %v", compact.RequiredArgs)
	}
	if compact.Cost != "cheap" {
		t.Fatalf("expected cost cheap, got %s", compact.Cost)
	}
}

func TestToCompactTool_DescriptionTruncation(t *testing.T) {
	longDesc := ""
	for i := 0; i < 150; i++ {
		longDesc += "x"
	}
	cap := &domain.Capability{
		Name:        "test.tool",
		Description: longDesc,
	}
	compact := toCompactTool(cap)
	if len(compact.Description) > 120 {
		t.Fatalf("expected description truncated to <=120, got %d", len(compact.Description))
	}
	if compact.Description[len(compact.Description)-3:] != "..." {
		t.Fatal("expected truncated description to end with ...")
	}
}
