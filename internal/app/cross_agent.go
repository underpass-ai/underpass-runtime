package app

import (
	"context"
	"fmt"
)

// CrossAgentInsight summarizes what the learning loop has observed across
// all agents operating in the same context signature. Attached to
// recommendation responses so agents know how much collective experience
// backs each suggestion.
type CrossAgentInsight struct {
	ContextSignature string        `json:"context_signature"`
	TotalInvocations int           `json:"total_invocations"`
	ToolCount        int           `json:"tool_count"`
	TopTools         []ToolInsight `json:"top_tools"`
	PolicyActive     bool          `json:"policy_active"`
	AlgorithmTier    string        `json:"algorithm_tier"` // heuristic, telemetry, thompson, neural_ts
	Confidence       string        `json:"confidence"`     // low, medium, high
}

// ToolInsight is a per-tool summary from cross-agent telemetry.
type ToolInsight struct {
	ToolID       string  `json:"tool_id"`
	Invocations  int     `json:"invocations"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMS int64   `json:"avg_latency_ms"`
}

// BuildCrossAgentInsight derives collective learning insights from the
// telemetry and policy data available for a context.
func BuildCrossAgentInsight(
	contextSig string,
	allStats map[string]ToolStats,
	policies map[string]ToolPolicy,
	algorithmID string,
) CrossAgentInsight {
	insight := CrossAgentInsight{
		ContextSignature: contextSig,
		PolicyActive:     len(policies) > 0,
	}

	// Aggregate stats across all tools
	for toolID, stats := range allStats {
		if stats.InvocationN == 0 {
			continue
		}
		insight.TotalInvocations += stats.InvocationN
		insight.ToolCount++
		insight.TopTools = append(insight.TopTools, ToolInsight{
			ToolID:       toolID,
			Invocations:  stats.InvocationN,
			SuccessRate:  stats.SuccessRate,
			AvgLatencyMS: stats.P50Duration,
		})
	}

	// Sort by invocations descending, keep top 5
	sortToolInsights(insight.TopTools)
	if len(insight.TopTools) > 5 {
		insight.TopTools = insight.TopTools[:5]
	}

	// Derive algorithm tier
	switch algorithmID {
	case AlgorithmIDHeuristic:
		insight.AlgorithmTier = "heuristic"
	case "beta_thompson_sampling":
		insight.AlgorithmTier = "thompson"
	case "neural_thompson_sampling":
		insight.AlgorithmTier = "neural_ts"
	default:
		if insight.TotalInvocations >= 5 {
			insight.AlgorithmTier = "telemetry"
		} else {
			insight.AlgorithmTier = "heuristic"
		}
	}

	// Derive confidence level
	switch {
	case insight.TotalInvocations >= 100 && insight.PolicyActive:
		insight.Confidence = "high"
	case insight.TotalInvocations >= 20:
		insight.Confidence = "medium"
	default:
		insight.Confidence = "low"
	}

	return insight
}

// InsightSummary returns a human-readable one-liner for the insight.
func (i CrossAgentInsight) InsightSummary() string {
	if i.TotalInvocations == 0 {
		return "no cross-agent data available"
	}
	return fmt.Sprintf("backed by %d invocations across %d tools (%s confidence, %s tier)",
		i.TotalInvocations, i.ToolCount, i.Confidence, i.AlgorithmTier)
}

// GetCrossAgentInsight returns learning insights for a context signature.
func (s *Service) GetCrossAgentInsight(ctx context.Context, sessionID string) (CrossAgentInsight, *ServiceError) {
	session, found, err := s.workspace.GetSession(ctx, sessionID)
	if err != nil || !found {
		return CrossAgentInsight{}, &ServiceError{Code: "not_found", Message: "session not found"}
	}

	digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
	contextSig := DeriveContextSignature(session, "", digest)

	allStats, _ := s.telemetryQ.AllToolStats(ctx)

	var policies map[string]ToolPolicy
	if s.policyLearned != nil {
		policies, _ = s.policyLearned.ReadPoliciesForContext(ctx, contextSig)
	}

	algorithmID := AlgorithmIDHeuristic
	scorer := SelectScorerWithModel(policies, nil)
	if scorer != nil {
		switch scorer.(type) {
		case ThompsonScorer:
			algorithmID = "beta_thompson_sampling"
		case NeuralTSScorer:
			algorithmID = "neural_thompson_sampling"
		}
	}

	return BuildCrossAgentInsight(contextSig, allStats, policies, algorithmID), nil
}

func sortToolInsights(tools []ToolInsight) {
	for i := 1; i < len(tools); i++ {
		for j := i; j > 0 && tools[j].Invocations > tools[j-1].Invocations; j-- {
			tools[j], tools[j-1] = tools[j-1], tools[j]
		}
	}
}
