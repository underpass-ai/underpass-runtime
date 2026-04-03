package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// WarmCache holds pre-loaded data for a session to accelerate the first
// RecommendTools call. Data is loaded in background after session creation.
type WarmCache struct {
	mu          sync.RWMutex
	ready       bool
	contextSig  string
	policies    map[string]ToolPolicy
	allStats    map[string]ToolStats
	neuralModel []byte
	warmedAt    time.Time
}

// IsReady returns true once background loading has completed.
func (c *WarmCache) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

// sessionWarmCaches is a per-session prewarm cache.
// Cleaned up when sessions are closed.
type sessionWarmCaches struct {
	mu     sync.RWMutex
	caches map[string]*WarmCache
}

func newSessionWarmCaches() *sessionWarmCaches {
	return &sessionWarmCaches{caches: map[string]*WarmCache{}}
}

func (s *sessionWarmCaches) get(sessionID string) (*WarmCache, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.caches[sessionID]
	return c, ok
}

func (s *sessionWarmCaches) evict(sessionID string) {
	s.mu.Lock()
	delete(s.caches, sessionID)
	s.mu.Unlock()
}

// prewarmSession kicks off background loading of policies, telemetry stats,
// and neural model for the given session context. Non-blocking.
func (svc *Service) prewarmSession(session domain.Session) {
	if svc.warmCaches == nil {
		return
	}

	cache := &WarmCache{}
	svc.warmCaches.mu.Lock()
	svc.warmCaches.caches[session.ID] = cache
	svc.warmCaches.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Derive context signature
		digest := BuildContextDigest(ctx, session.WorkspacePath, nil, nil)
		contextSig := DeriveContextSignature(session, "", digest)

		// Load policies
		var policies map[string]ToolPolicy
		if svc.policyLearned != nil {
			policies, _ = svc.policyLearned.ReadPoliciesForContext(ctx, contextSig)
		}

		// Load telemetry stats
		var allStats map[string]ToolStats
		if svc.telemetryQ != nil {
			allStats, _ = svc.telemetryQ.AllToolStats(ctx)
		}

		// Load neural model
		var modelData []byte
		if svc.neuralModel != nil {
			modelData, _, _ = svc.neuralModel.ReadNeuralModel(ctx, NeuralModelValkeyKey)
		}

		cache.mu.Lock()
		cache.contextSig = contextSig
		cache.policies = policies
		cache.allStats = allStats
		cache.neuralModel = modelData
		cache.warmedAt = time.Now().UTC()
		cache.ready = true
		cache.mu.Unlock()

		slog.Debug("session prewarmed",
			"session_id", session.ID,
			"context_sig", contextSig,
			"policies", len(policies),
			"stats", len(allStats),
			"has_model", len(modelData) > 0,
		)
	}()
}

// getWarmData returns prewarmed data if available, or nil if not yet ready.
func (svc *Service) getWarmData(sessionID string) (map[string]ToolPolicy, map[string]ToolStats, []byte, bool) {
	if svc.warmCaches == nil {
		return nil, nil, nil, false
	}
	cache, ok := svc.warmCaches.get(sessionID)
	if !ok || !cache.IsReady() {
		return nil, nil, nil, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.policies, cache.allStats, cache.neuralModel, true
}
