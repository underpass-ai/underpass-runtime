package policy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// ValkeyPolicyReader reads learned tool policies from Valkey.
// Keys follow the format: {prefix}:{context_signature}:{tool_id}
// Values are JSON-encoded app.ToolPolicy structs written by tool-learning.
type ValkeyPolicyReader struct {
	client *redis.Client
	prefix string
}

// NewValkeyPolicyReader creates a reader connected to the given Valkey instance.
func NewValkeyPolicyReader(addr, password string, db int, prefix string, tlsCfg *tls.Config) (*ValkeyPolicyReader, error) {
	opts := &redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	}
	if tlsCfg != nil {
		opts.TLSConfig = tlsCfg
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("valkey policy reader ping: %w", err)
	}
	if prefix == "" {
		prefix = "tool_policy"
	}
	return &ValkeyPolicyReader{client: client, prefix: prefix}, nil
}

// ReadPolicy reads a single policy for (contextSig, toolID).
func (r *ValkeyPolicyReader) ReadPolicy(ctx context.Context, contextSig, toolID string) (app.ToolPolicy, bool, error) {
	key := r.prefix + ":" + contextSig + ":" + toolID
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return app.ToolPolicy{}, false, nil
	}
	if err != nil {
		return app.ToolPolicy{}, false, fmt.Errorf("read policy %s: %w", key, err)
	}
	var p app.ToolPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return app.ToolPolicy{}, false, fmt.Errorf("unmarshal policy %s: %w", key, err)
	}
	return p, true, nil
}

// ReadPoliciesForContext reads all policies for a given context signature.
// Uses SCAN to iterate keys matching {prefix}:{contextSig}:*.
func (r *ValkeyPolicyReader) ReadPoliciesForContext(ctx context.Context, contextSig string) (map[string]app.ToolPolicy, error) {
	pattern := r.prefix + ":" + contextSig + ":*"
	policies := make(map[string]app.ToolPolicy)

	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan policies %s: %w", pattern, err)
		}
		for _, key := range keys {
			data, err := r.client.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}
			var p app.ToolPolicy
			if err := json.Unmarshal(data, &p); err != nil {
				continue
			}
			// Extract tool ID from key: prefix:contextSig:toolID
			toolID := p.ToolID
			if toolID == "" {
				toolID = strings.TrimPrefix(key, r.prefix+":"+contextSig+":")
			}
			policies[toolID] = p
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return policies, nil
}

// ReadNeuralModel reads a raw value from Valkey by key (e.g., model weights).
// Implements app.NeuralModelReader.
func (r *ValkeyPolicyReader) ReadNeuralModel(ctx context.Context, key string) ([]byte, bool, error) {
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read neural model %s: %w", key, err)
	}
	return data, true, nil
}

// Close releases the Valkey connection.
func (r *ValkeyPolicyReader) Close() error {
	return r.client.Close()
}
