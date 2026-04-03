package domain

import "time"

// RecommendationDecision is the persisted evidence record for a single
// RecommendTools call. It links the ranked output to the algorithm, policy
// mode, context, and the emitted event — forming the minimum auditable unit
// for the learning evidence plane.
type RecommendationDecision struct {
	RecommendationID string    `json:"recommendation_id"`
	SessionID        string    `json:"session_id"`
	TenantID         string    `json:"tenant_id"`
	ActorID          string    `json:"actor_id"`
	TaskHint         string    `json:"task_hint"`
	TopK             int       `json:"top_k"`
	ContextSignature string    `json:"context_signature"`
	DecisionSource   string    `json:"decision_source"`
	AlgorithmID      string    `json:"algorithm_id"`
	AlgorithmVersion string    `json:"algorithm_version"`
	PolicyMode       string    `json:"policy_mode"`
	CandidateCount   int       `json:"candidate_count"`
	EventID          string    `json:"event_id"`
	EventSubject     string    `json:"event_subject"`
	CreatedAt        time.Time `json:"created_at"`

	Recommendations []RankedToolEvidence `json:"recommendations"`
}

// RankedToolEvidence captures per-tool scoring evidence within a decision.
type RankedToolEvidence struct {
	ToolID         string           `json:"tool_id"`
	Rank           int              `json:"rank"`
	FinalScore     float64          `json:"final_score"`
	Why            string           `json:"why"`
	EstimatedCost  string           `json:"estimated_cost"`
	ScoreBreakdown []ScoreComponent `json:"score_breakdown,omitempty"`
}

// ScoreComponent is one named contribution to the final score.
type ScoreComponent struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Rationale string  `json:"rationale"`
}
