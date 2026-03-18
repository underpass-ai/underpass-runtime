package app

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// DiscoveryDetail controls the level of detail in discovery responses.
type DiscoveryDetail string

const (
	DiscoveryDetailCompact DiscoveryDetail = "compact"
	DiscoveryDetailFull    DiscoveryDetail = "full"
)

// CompactTool is the LLM-optimized representation of a capability.
type CompactTool struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	RequiredArgs []string `json:"required_args"`
	Risk         string   `json:"risk"`
	SideEffects  string   `json:"side_effects"`
	Approval     bool     `json:"approval"`
	Tags         []string `json:"tags"`
	Cost         string   `json:"cost"`
}

// FullTool is the documentation-grade representation of a capability,
// including all metadata, policy fields, and optional telemetry stats.
type FullTool struct {
	domain.Capability
	Tags  []string   `json:"tags"`
	Cost  string     `json:"cost"`
	Stats *ToolStats `json:"stats,omitempty"`
}

// ToolStats holds aggregated telemetry per tool. Populated by Phase 6 (WS-TEL-002).
type ToolStats struct {
	SuccessRate float64 `json:"success_rate"`
	P50Duration int64   `json:"p50_duration_ms"`
	P95Duration int64   `json:"p95_duration_ms"`
	AvgOutputKB float64 `json:"avg_output_kb"`
	DenyRate    float64 `json:"deny_rate"`
	InvocationN int     `json:"invocation_count"`
}

// DiscoveryResponse is returned by the discovery endpoint.
// Tools is []CompactTool when detail=compact, []FullTool when detail=full.
type DiscoveryResponse struct {
	Tools    any `json:"tools"`
	Total    int `json:"total"`
	Filtered int `json:"filtered"`
}

// DiscoveryFilter controls which tools are returned by DiscoverTools.
// All non-empty fields are AND-combined. Each multi-value field is OR-combined
// within itself (e.g., Risk=["low","medium"] matches tools with risk low OR medium).
type DiscoveryFilter struct {
	Risk        []string // low, medium, high
	Tags        []string // family prefix or scope (e.g., "fs", "repo")
	SideEffects []string // none, reversible, irreversible
	Scope       []string // repo, workspace, cluster, external
	Cost        []string // cheap, medium, expensive
}

// IsEmpty returns true when no filter criteria are set.
func (f DiscoveryFilter) IsEmpty() bool {
	return len(f.Risk) == 0 && len(f.Tags) == 0 && len(f.SideEffects) == 0 &&
		len(f.Scope) == 0 && len(f.Cost) == 0
}

// DiscoverTools returns a filtered view of available tools at the requested detail level.
func (s *Service) DiscoverTools(ctx context.Context, sessionID string, detail DiscoveryDetail, filter DiscoveryFilter) (DiscoveryResponse, *ServiceError) {
	tools, serviceErr := s.ListTools(ctx, sessionID)
	if serviceErr != nil {
		return DiscoveryResponse{}, serviceErr
	}

	total := len(s.catalog.List())

	if s.kpiMetrics != nil {
		s.kpiMetrics.ObserveDiscoveryRequest()
	}

	if detail == DiscoveryDetailFull {
		return s.discoverFull(tools, total, filter), nil
	}
	return discoverCompact(tools, total, filter), nil
}

func discoverCompact(tools []domain.Capability, total int, filter DiscoveryFilter) DiscoveryResponse {
	compact := make([]CompactTool, 0, len(tools))
	for i := range tools {
		ct := toCompactTool(&tools[i])
		if matchesFilter(ct, filter) {
			compact = append(compact, ct)
		}
	}
	sort.Slice(compact, func(i, j int) bool {
		return compact[i].Name < compact[j].Name
	})
	return DiscoveryResponse{Tools: compact, Total: total, Filtered: len(compact)}
}

func (s *Service) discoverFull(tools []domain.Capability, total int, filter DiscoveryFilter) DiscoveryResponse {
	// Load telemetry stats to populate the stats block in full detail.
	allStats, _ := s.telemetryQ.AllToolStats(context.Background())

	full := make([]FullTool, 0, len(tools))
	for i := range tools {
		ct := toCompactTool(&tools[i])
		if !matchesFilter(ct, filter) {
			continue
		}
		ft := toFullTool(&tools[i], ct.Tags, ct.Cost)
		if allStats != nil {
			if ts, ok := allStats[tools[i].Name]; ok && ts.InvocationN > 0 {
				ft.Stats = &ts
			}
		}
		full = append(full, ft)
	}
	sort.Slice(full, func(i, j int) bool {
		return full[i].Name < full[j].Name
	})
	return DiscoveryResponse{Tools: full, Total: total, Filtered: len(full)}
}

func toFullTool(cap *domain.Capability, tags []string, cost string) FullTool {
	return FullTool{
		Capability: *cap,
		Tags:       tags,
		Cost:       cost,
	}
}

// matchesFilter checks whether a compact tool passes all filter criteria.
func matchesFilter(ct CompactTool, f DiscoveryFilter) bool {
	if len(f.Risk) > 0 && !slices.Contains(f.Risk, ct.Risk) {
		return false
	}
	if len(f.SideEffects) > 0 && !slices.Contains(f.SideEffects, ct.SideEffects) {
		return false
	}
	if len(f.Cost) > 0 && !slices.Contains(f.Cost, ct.Cost) {
		return false
	}
	if len(f.Scope) > 0 && !hasAnyTag(ct.Tags, f.Scope) {
		return false
	}
	if len(f.Tags) > 0 && !hasAnyTag(ct.Tags, f.Tags) {
		return false
	}
	return true
}

// hasAnyTag reports whether any of wanted appears in tags.
func hasAnyTag(tags, wanted []string) bool {
	for _, w := range wanted {
		if slices.Contains(tags, w) {
			return true
		}
	}
	return false
}

func toCompactTool(cap *domain.Capability) CompactTool {
	desc := cap.Description
	if len(desc) > 120 {
		desc = desc[:117] + "..."
	}

	return CompactTool{
		Name:         cap.Name,
		Description:  desc,
		RequiredArgs: extractRequiredArgs(cap.InputSchema),
		Risk:         string(cap.RiskLevel),
		SideEffects:  string(cap.SideEffects),
		Approval:     cap.RequiresApproval,
		Tags:         deriveTags(cap),
		Cost:         deriveCost(cap),
	}
}

// extractRequiredArgs parses the input_schema JSON and returns the "required" field names.
func extractRequiredArgs(schema json.RawMessage) []string {
	if len(schema) == 0 {
		return []string{}
	}
	var parsed struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return []string{}
	}
	if parsed.Required == nil {
		return []string{}
	}
	return parsed.Required
}

// deriveTags extracts tags from the tool name's family prefix and scope.
func deriveTags(cap *domain.Capability) []string {
	tags := make([]string, 0, 3)
	if idx := strings.IndexByte(cap.Name, '.'); idx > 0 {
		tags = append(tags, cap.Name[:idx])
	}
	if cap.Scope != "" {
		tags = append(tags, string(cap.Scope))
	}
	return tags
}

// deriveCost estimates a cost hint from capability metadata.
func deriveCost(cap *domain.Capability) string {
	if cap.CostHint != "" {
		return cap.CostHint
	}
	if cap.Constraints.TimeoutSeconds <= 10 {
		return "cheap"
	}
	if cap.Constraints.TimeoutSeconds <= 60 {
		return "medium"
	}
	return "expensive"
}
