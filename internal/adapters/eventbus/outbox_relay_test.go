package eventbus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// fakeDownstream captures forwarded events and optionally injects errors.
type fakeDownstream struct {
	mu     sync.Mutex
	events []domain.DomainEvent
	err    error
}

func (f *fakeDownstream) Publish(_ context.Context, event domain.DomainEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

func (f *fakeDownstream) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func TestOutboxRelay_ForwardsEvents(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "relay-test")
	ds := &fakeDownstream{}

	ctx := context.Background()
	for _, id := range []string{"e1", "e2", "e3"} {
		_ = pub.Publish(ctx, makeTestEvent(t, id))
	}

	relay := NewOutboxRelay(pub, ds, nil,
		WithPollInterval(10*time.Millisecond),
		WithBatchSize(10),
	)
	relay.Start()

	// Wait for relay to process
	deadline := time.After(2 * time.Second)
	for ds.count() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events, got %d", ds.count())
		case <-time.After(10 * time.Millisecond):
		}
	}

	relay.Stop()

	if ds.count() != 3 {
		t.Fatalf("expected 3 forwarded events, got %d", ds.count())
	}

	// Outbox should be empty after ack
	n, _ := pub.Len(ctx)
	if n != 0 {
		t.Fatalf("expected 0 remaining in outbox, got %d", n)
	}
}

func TestOutboxRelay_StopWithoutStart(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}
	relay := NewOutboxRelay(pub, ds, nil)

	// Should not panic or block
	relay.Stop()
}

func TestOutboxRelay_DoubleStart(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}
	relay := NewOutboxRelay(pub, ds, nil, WithPollInterval(10*time.Millisecond))

	relay.Start()
	relay.Start() // second call should be no-op
	relay.Stop()
}

func TestOutboxRelay_BackoffOnDownstreamError(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ctx := context.Background()
	_ = pub.Publish(ctx, makeTestEvent(t, "e1"))

	var attempts atomic.Int64
	ds := &fakeDownstream{err: errors.New("downstream unavailable")}

	relay := NewOutboxRelay(pub, ds, nil,
		WithPollInterval(10*time.Millisecond),
		WithBatchSize(10),
	)

	// Override downstream to count attempts
	countingDS := &countingDownstream{inner: ds, attempts: &attempts}
	relay.downstream = countingDS

	relay.Start()
	time.Sleep(100 * time.Millisecond)
	relay.Stop()

	// Should have retried multiple times due to backoff
	if attempts.Load() < 2 {
		t.Fatalf("expected at least 2 retry attempts, got %d", attempts.Load())
	}

	// Event should still be in outbox (not acked)
	n, _ := pub.Len(ctx)
	if n != 1 {
		t.Fatalf("expected 1 event still in outbox, got %d", n)
	}
}

type countingDownstream struct {
	inner    *fakeDownstream
	attempts *atomic.Int64
}

func (c *countingDownstream) Publish(ctx context.Context, event domain.DomainEvent) error {
	c.attempts.Add(1)
	return c.inner.Publish(ctx, event)
}

func TestOutboxRelay_BackoffOnDrainError(t *testing.T) {
	fake := &fakeOutboxClient{ranErr: errors.New("valkey timeout")}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}

	relay := NewOutboxRelay(pub, ds, nil, WithPollInterval(10*time.Millisecond))
	relay.Start()
	time.Sleep(80 * time.Millisecond)
	relay.Stop()

	// No events should have been forwarded
	if ds.count() != 0 {
		t.Fatalf("expected 0 forwarded events, got %d", ds.count())
	}
}

func TestOutboxRelay_EmptyOutboxPolls(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}

	relay := NewOutboxRelay(pub, ds, nil, WithPollInterval(10*time.Millisecond))
	relay.Start()
	time.Sleep(50 * time.Millisecond)
	relay.Stop()

	// Nothing to forward, no errors expected
	if ds.count() != 0 {
		t.Fatalf("expected 0, got %d", ds.count())
	}
}

func TestOutboxRelay_AckErrorLogged(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ctx := context.Background()
	_ = pub.Publish(ctx, makeTestEvent(t, "e1"))

	// Inject trim error after the event is already in the list
	fake.trimErr = errors.New("ack failed")

	ds := &fakeDownstream{}
	relay := NewOutboxRelay(pub, ds, nil, WithPollInterval(10*time.Millisecond))
	relay.Start()
	time.Sleep(80 * time.Millisecond)
	relay.Stop()

	// Event forwarded to downstream but ack failed, so it stays in outbox
	// Downstream receives the event (possibly multiple times due to retries)
	if ds.count() < 1 {
		t.Fatalf("expected at least 1 forwarded event, got %d", ds.count())
	}
}

func TestOutboxRelay_Options(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}

	relay := NewOutboxRelay(pub, ds, nil,
		WithBatchSize(50),
		WithPollInterval(2*time.Second),
	)

	if relay.batchSize != 50 {
		t.Fatalf("expected batch size 50, got %d", relay.batchSize)
	}
	if relay.pollInterval != 2*time.Second {
		t.Fatalf("expected poll interval 2s, got %v", relay.pollInterval)
	}
}

func TestOutboxRelay_InvalidOptions(t *testing.T) {
	fake := &fakeOutboxClient{}
	pub := NewOutboxPublisher(fake, "test")
	ds := &fakeDownstream{}

	relay := NewOutboxRelay(pub, ds, nil,
		WithBatchSize(-1),
		WithPollInterval(-1),
	)

	if relay.batchSize != defaultBatchSize {
		t.Fatalf("expected default batch size, got %d", relay.batchSize)
	}
	if relay.pollInterval != defaultPollInterval {
		t.Fatalf("expected default poll interval, got %v", relay.pollInterval)
	}
}
