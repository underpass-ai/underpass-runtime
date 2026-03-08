package app

import (
	"encoding/json"
	"strings"
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

func TestMatchesFilter_EmptyFilterMatchesAll(t *testing.T) {
	ct := CompactTool{Risk: "high", SideEffects: "irreversible", Cost: "expensive", Tags: []string{"k8s", "cluster"}}
	if !matchesFilter(ct, DiscoveryFilter{}) {
		t.Fatal("empty filter should match all tools")
	}
}

func TestMatchesFilter_Risk(t *testing.T) {
	ct := CompactTool{Risk: "low", Tags: []string{"fs"}}
	if !matchesFilter(ct, DiscoveryFilter{Risk: []string{"low"}}) {
		t.Fatal("should match risk=low")
	}
	if matchesFilter(ct, DiscoveryFilter{Risk: []string{"high"}}) {
		t.Fatal("should not match risk=high")
	}
	if !matchesFilter(ct, DiscoveryFilter{Risk: []string{"low", "medium"}}) {
		t.Fatal("should match risk=low,medium (OR)")
	}
}

func TestMatchesFilter_SideEffects(t *testing.T) {
	ct := CompactTool{SideEffects: "none", Risk: "low", Tags: []string{"fs"}}
	if !matchesFilter(ct, DiscoveryFilter{SideEffects: []string{"none"}}) {
		t.Fatal("should match side_effects=none")
	}
	if matchesFilter(ct, DiscoveryFilter{SideEffects: []string{"reversible"}}) {
		t.Fatal("should not match side_effects=reversible")
	}
}

func TestMatchesFilter_Cost(t *testing.T) {
	ct := CompactTool{Cost: "cheap", Risk: "low", Tags: []string{"fs"}}
	if !matchesFilter(ct, DiscoveryFilter{Cost: []string{"cheap"}}) {
		t.Fatal("should match cost=cheap")
	}
	if matchesFilter(ct, DiscoveryFilter{Cost: []string{"expensive"}}) {
		t.Fatal("should not match cost=expensive")
	}
}

func TestMatchesFilter_Scope(t *testing.T) {
	ct := CompactTool{Tags: []string{"fs", "repo"}, Risk: "low"}
	if !matchesFilter(ct, DiscoveryFilter{Scope: []string{"repo"}}) {
		t.Fatal("should match scope=repo")
	}
	if matchesFilter(ct, DiscoveryFilter{Scope: []string{"cluster"}}) {
		t.Fatal("should not match scope=cluster")
	}
}

func TestMatchesFilter_Tags(t *testing.T) {
	ct := CompactTool{Tags: []string{"git", "repo"}, Risk: "low"}
	if !matchesFilter(ct, DiscoveryFilter{Tags: []string{"git"}}) {
		t.Fatal("should match tags=git")
	}
	if !matchesFilter(ct, DiscoveryFilter{Tags: []string{"repo", "workspace"}}) {
		t.Fatal("should match when any tag matches (OR)")
	}
	if matchesFilter(ct, DiscoveryFilter{Tags: []string{"k8s"}}) {
		t.Fatal("should not match tags=k8s")
	}
}

func TestMatchesFilter_ANDCombined(t *testing.T) {
	ct := CompactTool{Risk: "low", SideEffects: "none", Cost: "cheap", Tags: []string{"fs", "repo"}}
	f := DiscoveryFilter{Risk: []string{"low"}, SideEffects: []string{"none"}, Scope: []string{"repo"}}
	if !matchesFilter(ct, f) {
		t.Fatal("should match all AND conditions")
	}
	f2 := DiscoveryFilter{Risk: []string{"low"}, SideEffects: []string{"irreversible"}}
	if matchesFilter(ct, f2) {
		t.Fatal("should fail when any AND condition fails")
	}
}

func TestHasAnyTag(t *testing.T) {
	if !hasAnyTag([]string{"fs", "repo"}, []string{"fs"}) {
		t.Fatal("should find fs in tags")
	}
	if !hasAnyTag([]string{"fs", "repo"}, []string{"k8s", "repo"}) {
		t.Fatal("should find repo in tags")
	}
	if hasAnyTag([]string{"fs", "repo"}, []string{"k8s", "cluster"}) {
		t.Fatal("should not find k8s or cluster in tags")
	}
	if hasAnyTag([]string{}, []string{"fs"}) {
		t.Fatal("empty tags should not match anything")
	}
}

func TestDiscoveryFilter_IsEmpty(t *testing.T) {
	if !(DiscoveryFilter{}).IsEmpty() {
		t.Fatal("zero-value filter should be empty")
	}
	if (DiscoveryFilter{Risk: []string{"low"}}).IsEmpty() {
		t.Fatal("filter with risk should not be empty")
	}
	if (DiscoveryFilter{Tags: []string{"fs"}}).IsEmpty() {
		t.Fatal("filter with tags should not be empty")
	}
}

func TestToCompactTool_DescriptionTruncation(t *testing.T) {
	longDesc := strings.Repeat("x", 150)
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

func TestToFullTool(t *testing.T) {
	cap := &domain.Capability{
		Name:             "repo.test",
		Description:      "Run test suite for project",
		InputSchema:      json.RawMessage(`{"type":"object","required":["cmd"]}`),
		Scope:            domain.ScopeRepo,
		SideEffects:      domain.SideEffectsNone,
		RiskLevel:        domain.RiskLow,
		RequiresApproval: false,
		Idempotency:      domain.IdempotencyGuaranteed,
		Constraints:      domain.Constraints{TimeoutSeconds: 60, OutputLimitKB: 512},
		Preconditions:    []string{"repo cloned"},
		Observability:    domain.Observability{TraceName: "repo.test", SpanName: "test"},
	}
	tags := []string{"repo", "repo"}
	cost := "medium"

	ft := toFullTool(cap, tags, cost)
	if ft.Name != "repo.test" {
		t.Fatalf("expected name repo.test, got %s", ft.Name)
	}
	if ft.Description != "Run test suite for project" {
		t.Fatalf("expected full description, got %s", ft.Description)
	}
	if ft.RiskLevel != domain.RiskLow {
		t.Fatalf("expected risk_level low, got %s", ft.RiskLevel)
	}
	if ft.Constraints.TimeoutSeconds != 60 {
		t.Fatalf("expected timeout 60, got %d", ft.Constraints.TimeoutSeconds)
	}
	if ft.Constraints.OutputLimitKB != 512 {
		t.Fatalf("expected output_limit_kb 512, got %d", ft.Constraints.OutputLimitKB)
	}
	if len(ft.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(ft.Tags))
	}
	if ft.Cost != "medium" {
		t.Fatalf("expected cost medium, got %s", ft.Cost)
	}
	if ft.Stats != nil {
		t.Fatal("expected nil stats (not yet populated)")
	}
	if ft.Idempotency != domain.IdempotencyGuaranteed {
		t.Fatalf("expected idempotency guaranteed, got %s", ft.Idempotency)
	}
	if len(ft.Preconditions) != 1 {
		t.Fatalf("expected 1 precondition, got %d", len(ft.Preconditions))
	}
}
