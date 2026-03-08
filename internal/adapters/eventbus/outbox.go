package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// outboxClient defines the minimal Valkey operations used by the outbox.
type outboxClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	LTrim(ctx context.Context, key string, start, stop int64) *redis.StatusCmd
	LLen(ctx context.Context, key string) *redis.IntCmd
}

// OutboxPublisher persists events to a Valkey list before forwarding them to
// a downstream EventPublisher. This guarantees at-least-once delivery: events
// survive process crashes and are relayed asynchronously by the OutboxRelay.
type OutboxPublisher struct {
	client outboxClient
	key    string
}

// NewOutboxPublisher creates an OutboxPublisher backed by a Valkey client.
func NewOutboxPublisher(client outboxClient, keyPrefix string) *OutboxPublisher {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "workspace:outbox"
	}
	return &OutboxPublisher{client: client, key: prefix + ":pending"}
}

// NewOutboxPublisherFromAddress creates an OutboxPublisher connected to a
// Valkey instance at the given address.
func NewOutboxPublisherFromAddress(ctx context.Context, address, password string, db int, keyPrefix string) (*OutboxPublisher, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     strings.TrimSpace(address),
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("outbox valkey ping failed: %w", err)
	}
	return NewOutboxPublisher(client, keyPrefix), nil
}

// Publish appends the event to the outbox list. It does NOT forward to the
// downstream publisher — that is the relay's job.
func (p *OutboxPublisher) Publish(ctx context.Context, event domain.DomainEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal outbox event: %w", err)
	}
	if err := p.client.RPush(ctx, p.key, data).Err(); err != nil {
		return fmt.Errorf("rpush outbox event: %w", err)
	}
	return nil
}

// Len returns the number of events waiting in the outbox.
func (p *OutboxPublisher) Len(ctx context.Context) (int64, error) {
	n, err := p.client.LLen(ctx, p.key).Result()
	if err != nil {
		return 0, fmt.Errorf("llen outbox: %w", err)
	}
	return n, nil
}

// Drain reads up to batchSize events from the outbox without removing them.
// After the caller successfully processes them, call Ack to remove them.
func (p *OutboxPublisher) Drain(ctx context.Context, batchSize int64) ([]domain.DomainEvent, error) {
	raw, err := p.client.LRange(ctx, p.key, 0, batchSize-1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange outbox: %w", err)
	}
	events := make([]domain.DomainEvent, 0, len(raw))
	for _, s := range raw {
		var evt domain.DomainEvent
		if err := json.Unmarshal([]byte(s), &evt); err != nil {
			return nil, fmt.Errorf("unmarshal outbox event: %w", err)
		}
		events = append(events, evt)
	}
	return events, nil
}

// Ack removes the first n events from the outbox (LTRIM).
func (p *OutboxPublisher) Ack(ctx context.Context, n int64) error {
	if err := p.client.LTrim(ctx, p.key, n, -1).Err(); err != nil {
		return fmt.Errorf("ltrim outbox: %w", err)
	}
	return nil
}
