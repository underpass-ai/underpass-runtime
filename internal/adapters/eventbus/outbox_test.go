package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// fakeOutboxClient is a hand-written fake that satisfies the outboxClient interface.
type fakeOutboxClient struct {
	list    []string
	pingErr error
	pushErr error
	ranErr  error
	trimErr error
	lenErr  error
}

func (f *fakeOutboxClient) Ping(_ context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", f.pingErr)
}

func (f *fakeOutboxClient) RPush(_ context.Context, _ string, values ...interface{}) *redis.IntCmd {
	if f.pushErr != nil {
		return redis.NewIntResult(0, f.pushErr)
	}
	for _, v := range values {
		switch typed := v.(type) {
		case []byte:
			f.list = append(f.list, string(typed))
		case string:
			f.list = append(f.list, typed)
		default:
			data, _ := json.Marshal(typed)
			f.list = append(f.list, string(data))
		}
	}
	return redis.NewIntResult(int64(len(f.list)), nil)
}

func (f *fakeOutboxClient) LRange(_ context.Context, _ string, start, stop int64) *redis.StringSliceCmd {
	if f.ranErr != nil {
		return redis.NewStringSliceResult(nil, f.ranErr)
	}
	end := int(stop) + 1
	if end > len(f.list) {
		end = len(f.list)
	}
	s := int(start)
	if s >= len(f.list) {
		return redis.NewStringSliceResult(nil, nil)
	}
	return redis.NewStringSliceResult(f.list[s:end], nil)
}

func (f *fakeOutboxClient) LTrim(_ context.Context, _ string, start, _ int64) *redis.StatusCmd {
	if f.trimErr != nil {
		return redis.NewStatusResult("", f.trimErr)
	}
	s := int(start)
	if s >= len(f.list) {
		f.list = nil
	} else {
		f.list = f.list[s:]
	}
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeOutboxClient) LLen(_ context.Context, _ string) *redis.IntCmd {
	if f.lenErr != nil {
		return redis.NewIntResult(0, f.lenErr)
	}
	return redis.NewIntResult(int64(len(f.list)), nil)
}

func makeTestEvent(t *testing.T, id string) domain.DomainEvent {
	t.Helper()
	evt, err := domain.NewDomainEvent(id, domain.EventSessionCreated, "sess-1", "t1", "a1", domain.SessionCreatedPayload{
		RuntimeKind: domain.RuntimeKindDocker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return evt
}

func TestNewOutboxPublisher_DefaultPrefix(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "")
	if pub.key != "workspace:outbox:pending" {
		t.Fatalf("expected default key workspace:outbox:pending, got %s", pub.key)
	}
}

func TestNewOutboxPublisher_CustomPrefix(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "myapp:events")
	if pub.key != "myapp:events:pending" {
		t.Fatalf("expected key myapp:events:pending, got %s", pub.key)
	}
}

func TestNewOutboxPublisher_WhitespacePrefix(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "  ")
	if pub.key != "workspace:outbox:pending" {
		t.Fatalf("expected default key for whitespace prefix, got %s", pub.key)
	}
}

func TestOutboxPublisher_Publish(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	evt := makeTestEvent(t, "evt-1")

	if err := pub.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.list) != 1 {
		t.Fatalf("expected 1 item in list, got %d", len(fake.list))
	}

	var stored domain.DomainEvent
	if err := json.Unmarshal([]byte(fake.list[0]), &stored); err != nil {
		t.Fatalf("failed to unmarshal stored event: %v", err)
	}
	if stored.ID != "evt-1" {
		t.Fatalf("expected event id evt-1, got %s", stored.ID)
	}
}

func TestOutboxPublisher_PublishError(t *testing.T) {
	fake := &fakeOutboxClient{pushErr: errors.New("connection refused")}
	pub := NewOutboxPublisher(fake, "test")
	evt := makeTestEvent(t, "evt-err")

	err := pub.Publish(context.Background(), evt)
	if err == nil {
		t.Fatal("expected error from rpush")
	}
	if !errors.Is(err, fake.pushErr) {
		t.Fatalf("expected wrapped rpush error, got %v", err)
	}
}

func TestOutboxPublisher_Len(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")

	n, err := pub.Len(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	_ = pub.Publish(context.Background(), makeTestEvent(t, "e1"))
	_ = pub.Publish(context.Background(), makeTestEvent(t, "e2"))

	n, err = pub.Len(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestOutboxPublisher_LenError(t *testing.T) {
	fake := &fakeOutboxClient{lenErr: errors.New("redis down")}
	pub := NewOutboxPublisher(fake, "test")

	_, err := pub.Len(context.Background())
	if err == nil {
		t.Fatal("expected error from llen")
	}
}

func TestOutboxPublisher_DrainAndAck(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")

	for i := range 5 {
		evt := makeTestEvent(t, "evt-"+string(rune('a'+i)))
		if err := pub.Publish(context.Background(), evt); err != nil {
			t.Fatalf("publish error: %v", err)
		}
	}

	// Drain first 3
	events, err := pub.Drain(context.Background(), 3)
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Ack those 3
	if err := pub.Ack(context.Background(), 3); err != nil {
		t.Fatalf("ack error: %v", err)
	}

	n, _ := pub.Len(context.Background())
	if n != 2 {
		t.Fatalf("expected 2 remaining, got %d", n)
	}
}

func TestOutboxPublisher_DrainEmpty(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")

	events, err := pub.Drain(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestOutboxPublisher_DrainError(t *testing.T) {
	fake := &fakeOutboxClient{ranErr: errors.New("timeout")}
	pub := NewOutboxPublisher(fake, "test")

	_, err := pub.Drain(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error from lrange")
	}
}

func TestOutboxPublisher_DrainUnmarshalError(t *testing.T) {
	fake := &fakeOutboxClient{list: []string{"not-valid-json"}}
	pub := NewOutboxPublisher(fake, "test")

	_, err := pub.Drain(context.Background(), 10)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestOutboxPublisher_AckError(t *testing.T) {
	fake := &fakeOutboxClient{trimErr: errors.New("trim failed")}
	pub := NewOutboxPublisher(fake, "test")

	err := pub.Ack(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error from ltrim")
	}
}

func TestOutboxPublisher_AckAll(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")

	_ = pub.Publish(context.Background(), makeTestEvent(t, "e1"))
	_ = pub.Publish(context.Background(), makeTestEvent(t, "e2"))

	// Ack more than exists
	if err := pub.Ack(context.Background(), 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	n, _ := pub.Len(context.Background())
	if n != 0 {
		t.Fatalf("expected 0 remaining, got %d", n)
	}
}

func TestOutboxPublisher_FullCycle(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ctx := context.Background()

	// Publish 3 events
	for _, id := range []string{"evt-1", "evt-2", "evt-3"} {
		if err := pub.Publish(ctx, makeTestEvent(t, id)); err != nil {
			t.Fatalf("publish error: %v", err)
		}
	}

	// Drain batch of 2
	batch, err := pub.Drain(ctx, 2)
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2, got %d", len(batch))
	}

	// Ack 2
	if ackErr := pub.Ack(ctx, 2); ackErr != nil {
		t.Fatalf("ack error: %v", ackErr)
	}

	// Remaining should be 1
	remaining, err := pub.Drain(ctx, 10)
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
}
