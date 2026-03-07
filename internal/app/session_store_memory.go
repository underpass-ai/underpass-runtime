package app

import (
	"context"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type InMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]domain.Session
}

func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		sessions: map[string]domain.Session{},
	}
}

func (s *InMemorySessionStore) Save(_ context.Context, session domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	return nil
}

func (s *InMemorySessionStore) Get(_ context.Context, sessionID string) (domain.Session, bool, error) {
	s.mu.RLock()
	session, found := s.sessions[sessionID]
	s.mu.RUnlock()
	if !found {
		return domain.Session{}, false, nil
	}

	if !session.ExpiresAt.IsZero() && time.Now().UTC().After(session.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
		return domain.Session{}, false, nil
	}
	return session, true, nil
}

func (s *InMemorySessionStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}
