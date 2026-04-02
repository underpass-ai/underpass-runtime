package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	defaultTopK      = 10
	maxTopK          = 50
	baseScore        = 1.0
	riskPenaltyMed   = 0.15
	riskPenaltyHigh  = 0.35
	sideEffectPenRev = 0.10
	sideEffectPenIrr = 0.25
	approvalPenalty  = 0.10
	costPenMedium    = 0.05
	costPenExpensive = 0.15
	hintMatchBonus   = 0.20
)

// Recommendation is a single ranked tool suggestion.
type Recommendation struct {
	Name          string   `json:"name"`
	Score         float64  `json:"score"`
	Why           string   `json:"why"`
	EstimatedCost string   `json:"estimated_cost"`
	PolicyNotes   []string `json:"policy_notes"`
}

// RecommendationsResponse is returned by the recommendations endpoint.
type RecommendationsResponse struct {
	Recommendations []Recommendation `json:"recommendations"`
	TaskHint        string           `json:"task_hint"`
	TopK            int              `json:"top_k"`
}

const (
	// Telemetry-based scoring weights (WS-TEL-003)
	telSuccessBonus   = 0.15 // bonus for high success rate tools
	telSuccessMinN    = 5    // minimum invocations to apply success bonus
	telDurationPenP95 = 0.10 // penalty for tools in top p95 duration
	telDenyPenalty    = 0.10 // penalty for tools with high deny rate
	telDenyThreshold  = 0.20 // deny rate above this triggers penalty
)

// RecommendTools returns ranked tool recommendations based on static heuristics
// enhanced with telemetry-based scoring when available (WS-TEL-003).
// Policy-denied and runtime-unsupported tools are already excluded by ListTools.
func (s *Service) RecommendTools(ctx context.Context, sessionID string, taskHint string, topK int) (RecommendationsResponse, *ServiceError) {
	tools, serviceErr := s.ListTools(ctx, sessionID)
	if serviceErr != nil {
		return RecommendationsResponse{}, serviceErr
	}

	if topK <= 0 {
		topK = defaultTopK
	}
	if topK > maxTopK {
		topK = maxTopK
	}

	// Load telemetry stats for context-aware scoring
	allStats, _ := s.telemetryQ.AllToolStats(ctx)

	// Load learned policies for the current context (if available).
	var learnedPolicies map[string]ToolPolicy
	if s.policyLearned != nil {
		session, found, _ := s.workspace.GetSession(ctx, sessionID)
		if found {
			digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
			contextSig := DeriveContextSignature(session, "", digest)
			learnedPolicies, _ = s.policyLearned.ReadPoliciesForContext(ctx, contextSig)
		}
	}

	hintTokens := tokenize(taskHint)
	recs := make([]Recommendation, 0, len(tools))
	for i := range tools {
		rec := scoreTool(&tools[i], hintTokens)
		// Apply telemetry-based adjustments if stats exist
		if allStats != nil {
			rec = applyTelemetryBoost(rec, allStats[tools[i].Name])
		}
		// Apply learned policy scoring if available
		if p, ok := learnedPolicies[tools[i].Name]; ok {
			rec = applyLearnedPolicy(rec, p)
		}
		recs = append(recs, rec)
	}

	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Score != recs[j].Score {
			return recs[i].Score > recs[j].Score
		}
		return recs[i].Name < recs[j].Name
	})

	if topK < len(recs) {
		recs = recs[:topK]
	}

	return RecommendationsResponse{
		Recommendations: recs,
		TaskHint:        taskHint,
		TopK:            topK,
	}, nil
}

// applyTelemetryBoost adjusts a recommendation score using historical telemetry.
func applyTelemetryBoost(rec Recommendation, stats ToolStats) Recommendation {
	if stats.InvocationN < telSuccessMinN {
		return rec // not enough data
	}

	// Reward high success rate
	if stats.SuccessRate >= 0.90 {
		rec.Score += telSuccessBonus
		rec.Why += fmt.Sprintf(", %.0f%% success rate (%d invocations)", stats.SuccessRate*100, stats.InvocationN)
	} else if stats.SuccessRate < 0.50 {
		rec.Score -= telSuccessBonus
		rec.Why += fmt.Sprintf(", low success rate %.0f%%", stats.SuccessRate*100)
	}

	// Penalize tools with slow p95
	if stats.P95Duration > 10000 {
		rec.Score -= telDurationPenP95
		rec.Why += fmt.Sprintf(", slow p95 (%dms)", stats.P95Duration)
	}

	// Penalize tools with high deny rate
	if stats.DenyRate > telDenyThreshold {
		rec.Score -= telDenyPenalty
		rec.Why += fmt.Sprintf(", %.0f%% deny rate", stats.DenyRate*100)
	}

	rec.Score = math.Round(rec.Score*100) / 100
	return rec
}

// scoreTool applies static heuristic scoring to a capability.
func scoreTool(cap *domain.Capability, hintTokens []string) Recommendation {
	score := baseScore
	var reasons []string

	// Risk penalty
	switch cap.RiskLevel {
	case domain.RiskLow:
		reasons = append(reasons, "low risk")
	case domain.RiskMedium:
		score -= riskPenaltyMed
	case domain.RiskHigh:
		score -= riskPenaltyHigh
		reasons = append(reasons, "high risk (penalty)")
	}

	// Side effects penalty
	switch cap.SideEffects {
	case domain.SideEffectsNone:
		reasons = append(reasons, "no side effects")
	case domain.SideEffectsReversible:
		score -= sideEffectPenRev
	case domain.SideEffectsIrreversible:
		score -= sideEffectPenIrr
		reasons = append(reasons, "irreversible side effects (penalty)")
	}

	// Approval penalty
	if cap.RequiresApproval {
		score -= approvalPenalty
		reasons = append(reasons, "requires approval")
	}

	// Cost penalty
	cost := deriveCost(cap)
	switch cost {
	case "cheap":
		reasons = append(reasons, "low cost")
	case "medium":
		score -= costPenMedium
	case "expensive":
		score -= costPenExpensive
		reasons = append(reasons, "expensive (penalty)")
	}

	// Task hint matching bonus
	if len(hintTokens) > 0 {
		matchCount := countHintMatches(cap, hintTokens)
		if matchCount > 0 {
			bonus := hintMatchBonus * math.Min(float64(matchCount)/float64(len(hintTokens)), 1.0)
			score += bonus
			reasons = append(reasons, fmt.Sprintf("matches task hint (%d/%d tokens)", matchCount, len(hintTokens)))
		}
	}

	score = math.Round(score*100) / 100

	return Recommendation{
		Name:          cap.Name,
		Score:         score,
		Why:           strings.Join(reasons, ", "),
		EstimatedCost: cost,
		PolicyNotes:   []string{},
	}
}

// countHintMatches counts how many hint tokens appear in the tool's name or description.
func countHintMatches(cap *domain.Capability, tokens []string) int {
	nameLower := strings.ToLower(cap.Name)
	descLower := strings.ToLower(cap.Description)
	count := 0
	for _, tok := range tokens {
		if strings.Contains(nameLower, tok) || strings.Contains(descLower, tok) {
			count++
		}
	}
	return count
}

// tokenize splits a task hint into lowercase search tokens.
func tokenize(hint string) []string {
	if hint == "" {
		return nil
	}
	words := strings.Fields(strings.ToLower(hint))
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]{}") //nolint:gocritic // trim punctuation
		if len(w) >= 2 {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

const (
	// Learned policy scoring weights
	learnedConfidenceBoost    = 0.25 // max boost from Thompson confidence
	learnedErrorRatePenalty   = 0.20 // penalty for high error rate policies
	learnedErrorRateThreshold = 0.30 // error rate above this triggers penalty
	learnedLatencyPenalty     = 0.10 // penalty for slow policies
	learnedLatencyThreshold   = 15000 // p95 ms above this triggers penalty
	learnedMinSamples         = 10   // minimum samples before trusting policy
)

// applyLearnedPolicy adjusts a recommendation score using a learned policy
// from the tool-learning pipeline. Policies carry Thompson Sampling posterior
// parameters (Alpha, Beta) and aggregated performance metrics.
func applyLearnedPolicy(rec Recommendation, policy ToolPolicy) Recommendation {
	if policy.NSamples < learnedMinSamples {
		return rec
	}

	// Thompson confidence boost: higher confidence → higher score
	boost := learnedConfidenceBoost * policy.Confidence
	rec.Score += boost

	// Error rate penalty
	if policy.ErrorRate > learnedErrorRateThreshold {
		rec.Score -= learnedErrorRatePenalty
		rec.Why += fmt.Sprintf(", learned: high error rate %.0f%%", policy.ErrorRate*100)
	}

	// Latency penalty
	if policy.P95LatencyMs > learnedLatencyThreshold {
		rec.Score -= learnedLatencyPenalty
		rec.Why += fmt.Sprintf(", learned: slow p95 %dms", policy.P95LatencyMs)
	}

	rec.Why += fmt.Sprintf(", learned policy (confidence=%.2f, n=%d)", policy.Confidence, policy.NSamples)
	rec.Score = math.Round(rec.Score*100) / 100
	return rec
}

// deriveCost maps a capability's cost hint to a string label.
// Moved to the end to maintain method ordering.
