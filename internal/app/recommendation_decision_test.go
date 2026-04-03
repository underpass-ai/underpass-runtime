package app

import (
	"context"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ─── InMemoryRecommendationDecisionStore ────────────────────────────────────

func TestDecisionStore_SaveAndGet(t *testing.T) {
	store := NewInMemoryRecommendationDecisionStore()
	d := domain.RecommendationDecision{
		RecommendationID: "rec-1",
		SessionID:        "sess-1",
		DecisionSource:   DecisionSourceHeuristicOnly,
	}
	if err := store.Save(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Get(context.Background(), "rec-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected decision to be found")
	}
	if got.RecommendationID != "rec-1" {
		t.Fatalf("expected rec-1, got %s", got.RecommendationID)
	}
	if got.DecisionSource != DecisionSourceHeuristicOnly {
		t.Fatalf("expected %s, got %s", DecisionSourceHeuristicOnly, got.DecisionSource)
	}
}

func TestDecisionStore_GetMissing(t *testing.T) {
	store := NewInMemoryRecommendationDecisionStore()
	_, found, err := store.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

// ─── classifyDecisionSource ────────────────────────────────────────────────

func TestClassifyDecisionSource(t *testing.T) {
	tests := []struct {
		name     string
		stats    map[string]ToolStats
		policies map[string]ToolPolicy
		want     string
	}{
		{
			name: "no stats, no policies",
			want: DecisionSourceHeuristicOnly,
		},
		{
			name:  "stats only",
			stats: map[string]ToolStats{"fs.read_file": {InvocationN: 5}},
			want:  DecisionSourceHeuristicWithTelemetry,
		},
		{
			name:     "policies present",
			stats:    map[string]ToolStats{"fs.read_file": {InvocationN: 5}},
			policies: map[string]ToolPolicy{"fs.read_file": {NSamples: 20}},
			want:     DecisionSourceHeuristicWithPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyDecisionSource(tt.stats, tt.policies)
			if got != tt.want {
				t.Fatalf("classifyDecisionSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ─── RecommendTools with evidence ──────────────────────────────────────────

// capturingEventPublisher records published events for assertions.
type capturingEventPublisher struct {
	events []domain.DomainEvent
}

func (c *capturingEventPublisher) Publish(_ context.Context, event domain.DomainEvent) error {
	c.events = append(c.events, event)
	return nil
}

func makeEvidenceService() (*Service, *capturingEventPublisher) {
	svc := NewService(
		&fakeWorkspaceManager{
			session: domain.Session{
				ID:            testSessionID,
				WorkspacePath: "/tmp/ws",
				Principal:     domain.Principal{TenantID: "t1", ActorID: "a1"},
			},
			found: true,
		},
		&fakeCatalog{entries: map[string]domain.Capability{
			"fs.read_file": {
				Name:        "fs.read_file",
				RiskLevel:   domain.RiskLow,
				SideEffects: domain.SideEffectsNone,
				Constraints: domain.Constraints{TimeoutSeconds: 5},
			},
		}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
		&fakeAudit{},
	)
	eventPub := &capturingEventPublisher{}
	svc.SetEventPublisher(eventPub)
	return svc, eventPub
}

func TestRecommendTools_ReturnsBridgeFields(t *testing.T) {
	svc, _ := makeEvidenceService()

	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read file", 5)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}

	if resp.RecommendationID == "" {
		t.Fatal("expected non-empty recommendation_id")
	}
	if resp.EventID == "" {
		t.Fatal("expected non-empty event_id")
	}
	if resp.EventSubject != string(domain.EventRecommendationEmitted) {
		t.Fatalf("expected event_subject = %s, got %s", domain.EventRecommendationEmitted, resp.EventSubject)
	}
	if resp.DecisionSource != DecisionSourceHeuristicOnly {
		t.Fatalf("expected decision_source = %s, got %s", DecisionSourceHeuristicOnly, resp.DecisionSource)
	}
	if resp.AlgorithmID != AlgorithmIDHeuristic {
		t.Fatalf("expected algorithm_id = %s, got %s", AlgorithmIDHeuristic, resp.AlgorithmID)
	}
	if resp.AlgorithmVersion != AlgorithmVersionV1 {
		t.Fatalf("expected algorithm_version = %s, got %s", AlgorithmVersionV1, resp.AlgorithmVersion)
	}
	if resp.PolicyMode != PolicyModeNone {
		t.Fatalf("expected policy_mode = %s, got %s", PolicyModeNone, resp.PolicyMode)
	}
}

func TestRecommendTools_EmitsEvent(t *testing.T) {
	svc, eventPub := makeEvidenceService()

	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read", 5)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}

	if len(eventPub.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(eventPub.events))
	}

	evt := eventPub.events[0]
	if evt.Type != domain.EventRecommendationEmitted {
		t.Fatalf("expected event type %s, got %s", domain.EventRecommendationEmitted, evt.Type)
	}
	if evt.ID != resp.EventID {
		t.Fatalf("event ID %s does not match response event_id %s", evt.ID, resp.EventID)
	}
	if evt.SessionID != testSessionID {
		t.Fatalf("event session_id = %s, want %s", evt.SessionID, testSessionID)
	}
}

func TestRecommendTools_PersistsDecision(t *testing.T) {
	svc, _ := makeEvidenceService()

	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read", 5)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}

	decision, svcErr := svc.GetRecommendationDecision(context.Background(), resp.RecommendationID)
	if svcErr != nil {
		t.Fatalf("unexpected error getting decision: %v", svcErr)
	}
	if decision.RecommendationID != resp.RecommendationID {
		t.Fatalf("decision ID mismatch: got %s, want %s", decision.RecommendationID, resp.RecommendationID)
	}
	if decision.SessionID != testSessionID {
		t.Fatalf("decision session_id = %s, want %s", decision.SessionID, testSessionID)
	}
	if decision.EventID != resp.EventID {
		t.Fatalf("decision event_id mismatch: got %s, want %s", decision.EventID, resp.EventID)
	}
	if len(decision.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation in decision, got %d", len(decision.Recommendations))
	}
	if decision.Recommendations[0].ToolID != "fs.read_file" {
		t.Fatalf("expected tool fs.read_file, got %s", decision.Recommendations[0].ToolID)
	}
	if decision.Recommendations[0].Rank != 1 {
		t.Fatalf("expected rank 1, got %d", decision.Recommendations[0].Rank)
	}
}

// ─── GetRecommendationDecision ─────────────────────────────────────────────

func TestGetRecommendationDecision_NotFound(t *testing.T) {
	svc, _ := makeEvidenceService()

	_, svcErr := svc.GetRecommendationDecision(context.Background(), "nonexistent")
	if svcErr == nil {
		t.Fatal("expected error for nonexistent decision")
	}
	if svcErr.Code != "not_found" {
		t.Fatalf("expected not_found error code, got %s", svcErr.Code)
	}
}

// ─── GetEvidenceBundle ─────────────────────────────────────────────────────

func TestGetEvidenceBundle(t *testing.T) {
	svc, _ := makeEvidenceService()

	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read", 5)
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}

	bundle, svcErr := svc.GetEvidenceBundle(context.Background(), resp.RecommendationID)
	if svcErr != nil {
		t.Fatalf("unexpected error getting evidence bundle: %v", svcErr)
	}
	if bundle.Recommendation.RecommendationID != resp.RecommendationID {
		t.Fatal("bundle recommendation_id mismatch")
	}
	if bundle.Recommendation.DecisionSource != DecisionSourceHeuristicOnly {
		t.Fatalf("bundle decision_source = %s, want %s", bundle.Recommendation.DecisionSource, DecisionSourceHeuristicOnly)
	}
}

func TestGetEvidenceBundle_NotFound(t *testing.T) {
	svc, _ := makeEvidenceService()

	_, svcErr := svc.GetEvidenceBundle(context.Background(), "nonexistent")
	if svcErr == nil {
		t.Fatal("expected error for nonexistent bundle")
	}
	if svcErr.Code != "not_found" {
		t.Fatalf("expected not_found, got %s", svcErr.Code)
	}
}

// ─── GetLearningStatus ────────────────────────────────────────────────────

func TestGetLearningStatus(t *testing.T) {
	svc, _ := makeEvidenceService()
	st := svc.GetLearningStatus(context.Background())
	if st.Status != "active" {
		t.Fatalf("expected active, got %s", st.Status)
	}
	if !st.RecommendationEvents {
		t.Fatal("expected recommendation events enabled")
	}
	if !st.EvidenceProjection {
		t.Fatal("expected evidence projection enabled")
	}
	if len(st.ActiveAlgorithms) == 0 || st.ActiveAlgorithms[0] != "heuristic_v1" {
		t.Fatalf("expected heuristic_v1, got %v", st.ActiveAlgorithms)
	}
}

// ─── GetPolicy / ListPolicies ─────────────────────────────────────────────

func TestGetPolicy_NoPolicyReader(t *testing.T) {
	svc, _ := makeEvidenceService()
	_, svcErr := svc.GetPolicy(context.Background(), "ctx", "tool")
	if svcErr == nil || svcErr.Code != "not_found" {
		t.Fatalf("expected not_found when no policy reader, got %v", svcErr)
	}
}

func TestListPolicies_NoPolicyReader(t *testing.T) {
	svc, _ := makeEvidenceService()
	_, svcErr := svc.ListPolicies(context.Background(), "ctx")
	if svcErr == nil || svcErr.Code != "not_found" {
		t.Fatalf("expected not_found when no policy reader, got %v", svcErr)
	}
}

func TestGetPolicy_WithReader(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetPolicyReader(&fakePolicyReader{
		policy: ToolPolicy{ToolID: "fs.read_file", Alpha: 5, Beta: 1},
		found:  true,
	})
	pol, svcErr := svc.GetPolicy(context.Background(), "ctx", "fs.read_file")
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if pol.ToolID != "fs.read_file" {
		t.Fatalf("expected fs.read_file, got %s", pol.ToolID)
	}
}

func TestGetPolicy_NotFound(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetPolicyReader(&fakePolicyReader{found: false})
	_, svcErr := svc.GetPolicy(context.Background(), "ctx", "missing")
	if svcErr == nil || svcErr.Code != "not_found" {
		t.Fatalf("expected not_found, got %v", svcErr)
	}
}

func TestListPolicies_WithReader(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetPolicyReader(&fakePolicyReader{
		policies: map[string]ToolPolicy{
			"fs.read_file": {ToolID: "fs.read_file"},
		},
	})
	policies, svcErr := svc.ListPolicies(context.Background(), "ctx")
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
}

// ─── GetAggregate ─────────────────────────────────────────────────────────

func TestGetAggregate_NotFound(t *testing.T) {
	svc, _ := makeEvidenceService()
	_, svcErr := svc.GetAggregate(context.Background(), "missing")
	if svcErr == nil || svcErr.Code != "not_found" {
		t.Fatalf("expected not_found, got %v", svcErr)
	}
}

// ─── Fakes ────────────────────────────────────────────────────────────────

type fakePolicyReader struct {
	policy   ToolPolicy
	found    bool
	policies map[string]ToolPolicy
	err      error
}

func (f *fakePolicyReader) ReadPolicy(_ context.Context, _, _ string) (ToolPolicy, bool, error) {
	return f.policy, f.found, f.err
}
func (f *fakePolicyReader) ReadPoliciesForContext(_ context.Context, _ string) (map[string]ToolPolicy, error) {
	if f.policies == nil {
		return map[string]ToolPolicy{}, f.err
	}
	return f.policies, f.err
}

// ─── Agent Feedback Loop ──────────────────────────────────────────────────

func TestAcceptRecommendation(t *testing.T) {
	svc, eventPub := makeEvidenceService()

	eventID, svcErr := svc.AcceptRecommendation(context.Background(), testSessionID, "rec-1", "fs.read_file")
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}
	if len(eventPub.events) == 0 {
		t.Fatal("expected event published")
	}
	last := eventPub.events[len(eventPub.events)-1]
	if last.Type != domain.EventRecommendationAccepted {
		t.Fatalf("expected %s, got %s", domain.EventRecommendationAccepted, last.Type)
	}
}

func TestRejectRecommendation(t *testing.T) {
	svc, eventPub := makeEvidenceService()

	eventID, svcErr := svc.RejectRecommendation(context.Background(), testSessionID, "rec-1", "tool didn't help")
	if svcErr != nil {
		t.Fatalf("unexpected error: %v", svcErr)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}
	if len(eventPub.events) == 0 {
		t.Fatal("expected event published")
	}
	last := eventPub.events[len(eventPub.events)-1]
	if last.Type != domain.EventRecommendationRejected {
		t.Fatalf("expected %s, got %s", domain.EventRecommendationRejected, last.Type)
	}
}
