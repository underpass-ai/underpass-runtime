package app

import (
	"context"
	"sync"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// InMemoryRecommendationDecisionStore is an in-memory store for recommendation
// decisions, intended for local development and testing.
type InMemoryRecommendationDecisionStore struct {
	mu        sync.RWMutex
	decisions map[string]domain.RecommendationDecision
}

func NewInMemoryRecommendationDecisionStore() *InMemoryRecommendationDecisionStore {
	return &InMemoryRecommendationDecisionStore{
		decisions: make(map[string]domain.RecommendationDecision),
	}
}

func (s *InMemoryRecommendationDecisionStore) Save(_ context.Context, decision domain.RecommendationDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions[decision.RecommendationID] = decision
	return nil
}

func (s *InMemoryRecommendationDecisionStore) Get(_ context.Context, recommendationID string) (domain.RecommendationDecision, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.decisions[recommendationID]
	return d, ok, nil
}
