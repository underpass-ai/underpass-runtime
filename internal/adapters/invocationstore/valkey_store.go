package invocationstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type valkeyClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

type ValkeyStore struct {
	client    valkeyClient
	keyPrefix string
	ttl       time.Duration
}

type invocationEnvelope struct {
	ID            string                  `json:"id"`
	SessionID     string                  `json:"session_id"`
	ToolName      string                  `json:"tool_name"`
	CorrelationID string                  `json:"correlation_id,omitempty"`
	Status        domain.InvocationStatus `json:"status"`
	StartedAt     time.Time               `json:"started_at"`
	CompletedAt   *time.Time              `json:"completed_at,omitempty"`
	DurationMS    int64                   `json:"duration_ms"`
	TraceName     string                  `json:"trace_name"`
	SpanName      string                  `json:"span_name"`
	ExitCode      int                     `json:"exit_code"`
	OutputRef     string                  `json:"output_ref,omitempty"`
	LogsRef       string                  `json:"logs_ref,omitempty"`
	Artifacts     []domain.Artifact       `json:"artifacts,omitempty"`
	Error         *domain.Error           `json:"error,omitempty"`
}

func NewValkeyStore(client valkeyClient, keyPrefix string, ttl time.Duration) *ValkeyStore {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "workspace:invocation"
	}
	return &ValkeyStore{
		client:    client,
		keyPrefix: prefix,
		ttl:       ttl,
	}
}

func NewValkeyStoreFromAddress(
	ctx context.Context,
	address string,
	password string,
	db int,
	keyPrefix string,
	ttl time.Duration,
) (*ValkeyStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     strings.TrimSpace(address),
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping failed: %w", err)
	}
	return NewValkeyStore(client, keyPrefix, ttl), nil
}

func (s *ValkeyStore) Save(ctx context.Context, invocation domain.Invocation) error {
	data, err := json.Marshal(toEnvelope(invocation))
	if err != nil {
		return fmt.Errorf("marshal invocation: %w", err)
	}

	key := s.key(invocation.ID)
	if s.ttl > 0 {
		if err := s.client.Set(ctx, key, data, s.ttl).Err(); err != nil {
			return fmt.Errorf("set invocation: %w", err)
		}
	} else if err := s.client.Set(ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("set invocation: %w", err)
	}

	if corrKey := s.correlationKey(invocation.SessionID, invocation.ToolName, invocation.CorrelationID); corrKey != "" {
		if err := s.client.SetNX(ctx, corrKey, invocation.ID, s.ttl).Err(); err != nil {
			return fmt.Errorf("set invocation correlation index: %w", err)
		}
	}
	return nil
}

func (s *ValkeyStore) Get(ctx context.Context, invocationID string) (domain.Invocation, bool, error) {
	result, err := s.client.Get(ctx, s.key(invocationID)).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.Invocation{}, false, nil
		}
		return domain.Invocation{}, false, fmt.Errorf("get invocation: %w", err)
	}

	var envelope invocationEnvelope
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		return domain.Invocation{}, false, fmt.Errorf("unmarshal invocation: %w", err)
	}
	return fromEnvelope(envelope), true, nil
}

func (s *ValkeyStore) key(invocationID string) string {
	return s.keyPrefix + ":" + invocationID
}

func (s *ValkeyStore) FindByCorrelation(
	ctx context.Context,
	sessionID string,
	toolName string,
	correlationID string,
) (domain.Invocation, bool, error) {
	key := s.correlationKey(sessionID, toolName, correlationID)
	if key == "" {
		return domain.Invocation{}, false, nil
	}

	invocationID, err := s.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.Invocation{}, false, nil
		}
		return domain.Invocation{}, false, fmt.Errorf("get invocation correlation index: %w", err)
	}
	return s.Get(ctx, invocationID)
}

func (s *ValkeyStore) correlationKey(sessionID, toolName, correlationID string) string {
	sessionID = strings.TrimSpace(sessionID)
	toolName = strings.TrimSpace(toolName)
	correlationID = strings.TrimSpace(correlationID)
	if sessionID == "" || toolName == "" || correlationID == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s:corr:%s:%s:%s",
		s.keyPrefix,
		url.QueryEscape(sessionID),
		url.QueryEscape(toolName),
		url.QueryEscape(correlationID),
	)
}

func toEnvelope(invocation domain.Invocation) invocationEnvelope {
	return invocationEnvelope{
		ID:            invocation.ID,
		SessionID:     invocation.SessionID,
		ToolName:      invocation.ToolName,
		CorrelationID: invocation.CorrelationID,
		Status:        invocation.Status,
		StartedAt:     invocation.StartedAt,
		CompletedAt:   invocation.CompletedAt,
		DurationMS:    invocation.DurationMS,
		TraceName:     invocation.TraceName,
		SpanName:      invocation.SpanName,
		ExitCode:      invocation.ExitCode,
		OutputRef:     invocation.OutputRef,
		LogsRef:       invocation.LogsRef,
		Artifacts:     invocation.Artifacts,
		Error:         invocation.Error,
	}
}

func fromEnvelope(envelope invocationEnvelope) domain.Invocation {
	return domain.Invocation{
		ID:            envelope.ID,
		SessionID:     envelope.SessionID,
		ToolName:      envelope.ToolName,
		CorrelationID: envelope.CorrelationID,
		Status:        envelope.Status,
		StartedAt:     envelope.StartedAt,
		CompletedAt:   envelope.CompletedAt,
		DurationMS:    envelope.DurationMS,
		TraceName:     envelope.TraceName,
		SpanName:      envelope.SpanName,
		ExitCode:      envelope.ExitCode,
		OutputRef:     envelope.OutputRef,
		LogsRef:       envelope.LogsRef,
		Artifacts:     envelope.Artifacts,
		Error:         envelope.Error,
	}
}
