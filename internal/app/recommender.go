package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

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
	Name           string                  `json:"name"`
	Score          float64                 `json:"score"`
	Why            string                  `json:"why"`
	EstimatedCost  string                  `json:"estimated_cost"`
	PolicyNotes    []string                `json:"policy_notes"`
	ScoreBreakdown []domain.ScoreComponent `json:"score_breakdown,omitempty"`
}

// RecommendationsResponse is returned by the recommendations endpoint.
type RecommendationsResponse struct {
	Recommendations  []Recommendation   `json:"recommendations"`
	TaskHint         string             `json:"task_hint"`
	TopK             int                `json:"top_k"`
	RecommendationID string             `json:"recommendation_id"`
	EventID          string             `json:"event_id"`
	EventSubject     string             `json:"event_subject"`
	DecisionSource   string             `json:"decision_source"`
	AlgorithmID      string             `json:"algorithm_id"`
	AlgorithmVersion string             `json:"algorithm_version"`
	PolicyMode       string             `json:"policy_mode"`
	Insight          *CrossAgentInsight `json:"insight,omitempty"`
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
//
// P0 evidence: each call generates a RecommendationDecision, emits an event
// on runtime.learning.recommendation.emitted, and returns bridge fields so the
// caller can resolve the evidence through the learning evidence API.
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

	// Load session for context signature and event metadata.
	session, found, _ := s.workspace.GetSession(ctx, sessionID)
	if !found {
		return RecommendationsResponse{}, &ServiceError{Code: "not_found", Message: sessionNotFound}
	}

	// Try prewarmed data first, fall back to live loading.
	var allStats map[string]ToolStats
	var learnedPolicies map[string]ToolPolicy
	var neuralWeights *MLPWeights
	var contextSig string

	if warmPolicies, warmStats, warmModel, ok := s.getWarmData(sessionID); ok {
		learnedPolicies = warmPolicies
		allStats = warmStats
		if warmModel != nil {
			neuralWeights, _ = UnmarshalMLPWeights(warmModel)
		}
		digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
		contextSig = DeriveContextSignature(session, "", digest)
	} else {
		allStats, _ = s.telemetryQ.AllToolStats(ctx)
		digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
		contextSig = DeriveContextSignature(session, "", digest)
		if s.policyLearned != nil {
			learnedPolicies, _ = s.policyLearned.ReadPoliciesForContext(ctx, contextSig)
		}
		if s.neuralModel != nil {
			if data, found, err := s.neuralModel.ReadNeuralModel(ctx, NeuralModelValkeyKey); err == nil && found {
				neuralWeights, _ = UnmarshalMLPWeights(data)
			}
		}
	}

	// Select scoring algorithm based on available policy data + neural model + HyLinUCB.
	scorer := SelectScorerFull(learnedPolicies, neuralWeights, s.hylinucb, contextSig)

	// Derive algorithm metadata from the selected scorer.
	algorithmID := AlgorithmIDHeuristic
	algorithmVersion := AlgorithmVersionV1
	decisionSource := classifyDecisionSource(allStats, learnedPolicies)
	if scorer != nil {
		algorithmID = scorer.AlgorithmID()
		algorithmVersion = scorer.AlgorithmVersion()
		// Override decision source based on actual scorer.
		switch scorer.(type) {
		case NeuralTSScorer:
			decisionSource = DecisionSourceNeuralTS
		case ThompsonScorer:
			decisionSource = DecisionSourceThompson
		}
	}

	candidateCount := len(tools)
	hintTokens := tokenize(taskHint)
	recs := make([]Recommendation, 0, len(tools))
	for i := range tools {
		rec := scoreTool(&tools[i], hintTokens)
		baseScore := rec.Score
		rec.ScoreBreakdown = append(rec.ScoreBreakdown, domain.ScoreComponent{
			Name: "heuristic", Value: baseScore, Rationale: rec.Why,
		})

		// Apply telemetry-based adjustments if stats exist
		if allStats != nil {
			beforeTel := rec.Score
			rec = applyTelemetryBoost(rec, allStats[tools[i].Name])
			if delta := rec.Score - beforeTel; delta != 0 {
				rec.ScoreBreakdown = append(rec.ScoreBreakdown, domain.ScoreComponent{
					Name: "telemetry_boost", Value: delta, Rationale: fmt.Sprintf("%.2f → %.2f", beforeTel, rec.Score),
				})
			}
		}

		// Apply learned policy scoring via selected algorithm
		if p, ok := learnedPolicies[tools[i].Name]; ok && scorer != nil {
			beforePolicy := rec.Score
			newScore, why := scorer.Score(rec.Score, p)
			if why != "" {
				rec.Score = newScore
				rec.Why += ", " + why
				rec.PolicyNotes = append(rec.PolicyNotes,
					fmt.Sprintf("algorithm:%s policy:%s:%s confidence=%.2f n=%d",
						algorithmID, p.ContextSignature, p.ToolID, p.Confidence, p.NSamples),
				)
				rec.ScoreBreakdown = append(rec.ScoreBreakdown, domain.ScoreComponent{
					Name: algorithmID, Value: newScore - beforePolicy, Rationale: why,
				})
			}
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

	// --- P0 evidence: build decision, persist, emit event ---

	recID := newID("rec")
	eventID := newID("evt")
	eventSubject := string(domain.EventRecommendationEmitted)
	policyMode := PolicyModeNone
	if len(learnedPolicies) > 0 {
		policyMode = PolicyModeShadow
	}

	// Build ranked evidence.
	rankedEvidence := make([]domain.RankedToolEvidence, len(recs))
	rankedFacts := make([]domain.RankedToolFact, len(recs))
	for i, r := range recs {
		rankedEvidence[i] = domain.RankedToolEvidence{
			ToolID:         r.Name,
			Rank:           i + 1,
			FinalScore:     r.Score,
			Why:            r.Why,
			EstimatedCost:  r.EstimatedCost,
			ScoreBreakdown: r.ScoreBreakdown,
		}
		rankedFacts[i] = domain.RankedToolFact{
			ToolID:     r.Name,
			Rank:       i + 1,
			FinalScore: r.Score,
		}
	}

	decision := domain.RecommendationDecision{
		RecommendationID: recID,
		SessionID:        sessionID,
		TenantID:         session.Principal.TenantID,
		ActorID:          session.Principal.ActorID,
		TaskHint:         taskHint,
		TopK:             topK,
		ContextSignature: contextSig,
		DecisionSource:   decisionSource,
		AlgorithmID:      algorithmID,
		AlgorithmVersion: algorithmVersion,
		PolicyMode:       policyMode,
		CandidateCount:   candidateCount,
		EventID:          eventID,
		EventSubject:     eventSubject,
		CreatedAt:        time.Now().UTC(),
		Recommendations:  rankedEvidence,
	}

	// Persist decision (non-blocking on error).
	_ = s.decisionStore.Save(ctx, decision)

	// Emit recommendation event.
	payload := domain.RecommendationEmittedPayload{
		RecommendationID: recID,
		TaskHint:         taskHint,
		TopK:             topK,
		DecisionSource:   decisionSource,
		AlgorithmID:      algorithmID,
		AlgorithmVersion: algorithmVersion,
		PolicyMode:       policyMode,
		Tools:            rankedFacts,
	}
	evt, err := domain.NewDomainEvent(eventID, domain.EventRecommendationEmitted,
		sessionID, session.Principal.TenantID, session.Principal.ActorID, payload)
	if err == nil {
		_ = s.events.Publish(ctx, evt)
	}

	// Track recommended tools for KPI metrics (recommendation acceptance).
	toolIDs := make([]string, len(recs))
	for i := range recs {
		toolIDs[i] = recs[i].Name
	}
	s.sessionLastRec.Store(sessionID, toolIDs)

	// Build cross-agent insight.
	insight := BuildCrossAgentInsight(contextSig, allStats, learnedPolicies, algorithmID)

	return RecommendationsResponse{
		Recommendations:  recs,
		TaskHint:         taskHint,
		TopK:             topK,
		RecommendationID: recID,
		EventID:          eventID,
		EventSubject:     eventSubject,
		DecisionSource:   decisionSource,
		AlgorithmID:      algorithmID,
		AlgorithmVersion: algorithmVersion,
		PolicyMode:       policyMode,
		Insight:          &insight,
	}, nil
}

// classifyDecisionSource determines the active scoring tier label.
func classifyDecisionSource(stats map[string]ToolStats, policies map[string]ToolPolicy) string {
	if len(policies) > 0 {
		// Check if enough data for Thompson sampling.
		for _, p := range policies {
			if p.NSamples >= 50 {
				return DecisionSourceThompson
			}
		}
		return DecisionSourceHeuristicWithPolicy
	}
	if len(stats) > 0 {
		return DecisionSourceHeuristicWithTelemetry
	}
	return DecisionSourceHeuristicOnly
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
	// Evidence metadata constants
	AlgorithmIDHeuristic                 = "heuristic_v1"
	AlgorithmVersionV1                   = "1.0.0"
	PolicyModeNone                       = "none"
	PolicyModeShadow                     = "shadow"
	PolicyModeAssist                     = "assist"
	PolicyModeEnforced                   = "enforced"
	DecisionSourceHeuristicOnly          = "heuristic_only"
	DecisionSourceHeuristicWithTelemetry = "heuristic_with_telemetry"
	DecisionSourceHeuristicWithPolicy    = "heuristic_with_learned_policy"
	DecisionSourceThompson               = "learned_policy_thompson"
	DecisionSourceNeuralTS               = "neural_thompson_sampling"
)

const (
	// Learned policy scoring weights
	learnedConfidenceBoost    = 0.25  // max boost from Thompson confidence
	learnedErrorRatePenalty   = 0.20  // penalty for high error rate policies
	learnedErrorRateThreshold = 0.30  // error rate above this triggers penalty
	learnedLatencyPenalty     = 0.10  // penalty for slow policies
	learnedLatencyThreshold   = 15000 // p95 ms above this triggers penalty
	learnedMinSamples         = 10    // minimum samples before trusting policy
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
