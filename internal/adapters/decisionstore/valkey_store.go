package decisionstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type valkeyClient interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

// ValkeyStore persists RecommendationDecision records in Valkey with a
// configurable TTL, making them durable across service restarts.
type ValkeyStore struct {
	client    valkeyClient
	keyPrefix string
	ttl       time.Duration
}

// NewValkeyStore creates a ValkeyStore from an already-connected client.
func NewValkeyStore(client valkeyClient, keyPrefix string, ttl time.Duration) *ValkeyStore {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "workspace:decision"
	}
	return &ValkeyStore{
		client:    client,
		keyPrefix: prefix,
		ttl:       ttl,
	}
}

func (s *ValkeyStore) Save(ctx context.Context, decision domain.RecommendationDecision) error {
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal decision: %w", err)
	}
	key := s.key(decision.RecommendationID)
	if err := s.client.Set(ctx, key, data, s.ttl).Err(); err != nil {
		return fmt.Errorf("set decision: %w", err)
	}
	return nil
}

func (s *ValkeyStore) Get(ctx context.Context, recommendationID string) (domain.RecommendationDecision, bool, error) {
	result, err := s.client.Get(ctx, s.key(recommendationID)).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.RecommendationDecision{}, false, nil
		}
		return domain.RecommendationDecision{}, false, fmt.Errorf("get decision: %w", err)
	}

	var d domain.RecommendationDecision
	if err := json.Unmarshal([]byte(result), &d); err != nil {
		return domain.RecommendationDecision{}, false, fmt.Errorf("unmarshal decision: %w", err)
	}
	return d, true, nil
}

func (s *ValkeyStore) key(recommendationID string) string {
	return s.keyPrefix + ":" + recommendationID
}
