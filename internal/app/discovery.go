package app

import (
	"context"
	"encoding/json"
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

// DiscoveryResponse is returned by the discovery endpoint.
type DiscoveryResponse struct {
	Tools    []CompactTool `json:"tools"`
	Total    int           `json:"total"`
	Filtered int           `json:"filtered"`
}

// DiscoverTools returns an LLM-optimized compact view of available tools.
func (s *Service) DiscoverTools(ctx context.Context, sessionID string) (DiscoveryResponse, *ServiceError) {
	tools, serviceErr := s.ListTools(ctx, sessionID)
	if serviceErr != nil {
		return DiscoveryResponse{}, serviceErr
	}

	total := len(s.catalog.List())
	compact := make([]CompactTool, 0, len(tools))
	for i := range tools {
		compact = append(compact, toCompactTool(&tools[i]))
	}

	sort.Slice(compact, func(i, j int) bool {
		return compact[i].Name < compact[j].Name
	})

	return DiscoveryResponse{
		Tools:    compact,
		Total:    total,
		Filtered: len(compact),
	}, nil
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
