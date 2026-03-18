package valkey

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// PolicyStore implements app.PolicyStore using Valkey (Redis-compatible).
// Key format: {prefix}:{context_signature}:{tool_id}
type PolicyStore struct {
	client    redis.Cmdable
	keyPrefix string
	ttl       time.Duration
}

// NewPolicyStore creates a PolicyStore from an existing Redis client.
func NewPolicyStore(client redis.Cmdable, keyPrefix string, ttl time.Duration) *PolicyStore {
	return &PolicyStore{
		client:    client,
		keyPrefix: keyPrefix,
		ttl:       ttl,
	}
}

// NewPolicyStoreFromAddress creates a PolicyStore connecting to a Valkey address.
func NewPolicyStoreFromAddress(ctx context.Context, addr, password string, db int, keyPrefix string, ttl time.Duration, tlsCfg *tls.Config) (*PolicyStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:      addr,
		Password:  password,
		DB:        db,
		TLSConfig: tlsCfg,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping: %w", err)
	}
	return NewPolicyStore(client, keyPrefix, ttl), nil
}

func (s *PolicyStore) key(contextSig, toolID string) string {
	return s.keyPrefix + ":" + contextSig + ":" + toolID
}

// WritePolicy persists a single computed policy.
func (s *PolicyStore) WritePolicy(ctx context.Context, policy domain.ToolPolicy) error {
	data, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	return s.client.Set(ctx, s.key(policy.ContextSignature, policy.ToolID), data, s.ttl).Err()
}

// WritePolicies persists a batch of policies using a pipeline.
func (s *PolicyStore) WritePolicies(ctx context.Context, policies []domain.ToolPolicy) error {
	pipe := s.client.Pipeline()
	for _, p := range policies {
		data, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("marshal policy %s/%s: %w", p.ContextSignature, p.ToolID, err)
		}
		pipe.Set(ctx, s.key(p.ContextSignature, p.ToolID), data, s.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// ReadPolicy reads a single policy by context signature and tool ID.
func (s *PolicyStore) ReadPolicy(ctx context.Context, contextSig, toolID string) (domain.ToolPolicy, bool, error) {
	data, err := s.client.Get(ctx, s.key(contextSig, toolID)).Bytes()
	if err == redis.Nil {
		return domain.ToolPolicy{}, false, nil
	}
	if err != nil {
		return domain.ToolPolicy{}, false, fmt.Errorf("get policy: %w", err)
	}
	var policy domain.ToolPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return domain.ToolPolicy{}, false, fmt.Errorf("unmarshal policy: %w", err)
	}
	return policy, true, nil
}
