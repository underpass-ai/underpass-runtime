package app

import (
	"context"
	"strings"
	"sync"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type InMemoryInvocationStore struct {
	mu          sync.RWMutex
	invocations map[string]domain.Invocation
	byCorrKey   map[string]string
}

func NewInMemoryInvocationStore() *InMemoryInvocationStore {
	return &InMemoryInvocationStore{
		invocations: map[string]domain.Invocation{},
		byCorrKey:   map[string]string{},
	}
}

func (s *InMemoryInvocationStore) Save(_ context.Context, invocation domain.Invocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invocations[invocation.ID] = invocation
	if key := correlationLookupKey(invocation.SessionID, invocation.ToolName, invocation.CorrelationID); key != "" {
		if _, exists := s.byCorrKey[key]; !exists {
			s.byCorrKey[key] = invocation.ID
		}
	}
	return nil
}

func (s *InMemoryInvocationStore) Get(_ context.Context, invocationID string) (domain.Invocation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	invocation, ok := s.invocations[invocationID]
	return invocation, ok, nil
}

func (s *InMemoryInvocationStore) FindByCorrelation(
	_ context.Context,
	sessionID string,
	toolName string,
	correlationID string,
) (domain.Invocation, bool, error) {
	key := correlationLookupKey(sessionID, toolName, correlationID)
	if key == "" {
		return domain.Invocation{}, false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	invocationID, found := s.byCorrKey[key]
	if !found {
		return domain.Invocation{}, false, nil
	}
	invocation, ok := s.invocations[invocationID]
	return invocation, ok, nil
}

func correlationLookupKey(sessionID, toolName, correlationID string) string {
	sessionID = strings.TrimSpace(sessionID)
	toolName = strings.TrimSpace(toolName)
	correlationID = strings.TrimSpace(correlationID)
	if sessionID == "" || toolName == "" || correlationID == "" {
		return ""
	}
	return sessionID + "|" + toolName + "|" + correlationID
}
