package sessionstore

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
	Ping(ctx context.Context) *redis.StatusCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type ValkeyStore struct {
	client     valkeyClient
	keyPrefix  string
	defaultTTL time.Duration
}

func NewValkeyStore(client valkeyClient, keyPrefix string, defaultTTL time.Duration) *ValkeyStore {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "workspace:session"
	}
	return &ValkeyStore{
		client:     client,
		keyPrefix:  prefix,
		defaultTTL: defaultTTL,
	}
}

func NewValkeyStoreFromAddress(
	ctx context.Context,
	address string,
	password string,
	db int,
	keyPrefix string,
	defaultTTL time.Duration,
) (*ValkeyStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     strings.TrimSpace(address),
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping failed: %w", err)
	}
	return NewValkeyStore(client, keyPrefix, defaultTTL), nil
}

func (s *ValkeyStore) Save(ctx context.Context, session domain.Session) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	ttl := ttlFromSessionExpiry(session.ExpiresAt, s.defaultTTL)
	if err := s.client.Set(ctx, s.key(session.ID), payload, ttl).Err(); err != nil {
		return fmt.Errorf("set session: %w", err)
	}
	return nil
}

func (s *ValkeyStore) Get(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	value, err := s.client.Get(ctx, s.key(sessionID)).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.Session{}, false, nil
		}
		return domain.Session{}, false, fmt.Errorf("get session: %w", err)
	}

	var session domain.Session
	if err := json.Unmarshal([]byte(value), &session); err != nil {
		return domain.Session{}, false, fmt.Errorf("unmarshal session: %w", err)
	}
	if !session.ExpiresAt.IsZero() && time.Now().UTC().After(session.ExpiresAt) {
		_ = s.Delete(ctx, sessionID)
		return domain.Session{}, false, nil
	}
	return session, true, nil
}

func (s *ValkeyStore) Delete(ctx context.Context, sessionID string) error {
	if err := s.client.Del(ctx, s.key(sessionID)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *ValkeyStore) key(sessionID string) string {
	return s.keyPrefix + ":" + strings.TrimSpace(sessionID)
}

func ttlFromSessionExpiry(expiresAt time.Time, fallback time.Duration) time.Duration {
	now := time.Now().UTC()
	if !expiresAt.IsZero() {
		ttl := expiresAt.Sub(now)
		if ttl > 0 {
			return ttl
		}
		return time.Second
	}
	if fallback > 0 {
		return fallback
	}
	return 0
}
