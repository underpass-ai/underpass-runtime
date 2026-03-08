package eventbus

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

const (
	defaultBatchSize    = 100
	defaultPollInterval = 500 * time.Millisecond
	maxBackoff          = 30 * time.Second
	backoffMultiplier   = 2
)

// OutboxRelay drains events from an OutboxPublisher and forwards them to a
// downstream EventPublisher. It runs a single background goroutine with
// exponential backoff on transient failures.
type OutboxRelay struct {
	outbox       *OutboxPublisher
	downstream   app.EventPublisher
	logger       *slog.Logger
	batchSize    int64
	pollInterval time.Duration

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// OutboxRelayOption configures an OutboxRelay.
type OutboxRelayOption func(*OutboxRelay)

// WithBatchSize sets the maximum number of events drained per cycle.
func WithBatchSize(n int64) OutboxRelayOption {
	return func(r *OutboxRelay) {
		if n > 0 {
			r.batchSize = n
		}
	}
}

// WithPollInterval sets the polling interval between drain cycles.
func WithPollInterval(d time.Duration) OutboxRelayOption {
	return func(r *OutboxRelay) {
		if d > 0 {
			r.pollInterval = d
		}
	}
}

// NewOutboxRelay creates a relay that forwards events from the outbox to the
// downstream publisher. Call Start to begin processing.
func NewOutboxRelay(outbox *OutboxPublisher, downstream app.EventPublisher, logger *slog.Logger, opts ...OutboxRelayOption) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	r := &OutboxRelay{
		outbox:       outbox,
		downstream:   downstream,
		logger:       logger,
		batchSize:    defaultBatchSize,
		pollInterval: defaultPollInterval,
		done:         make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Start begins the background relay loop. It is safe to call multiple times;
// only the first call starts the goroutine.
func (r *OutboxRelay) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.running = true
	r.done = make(chan struct{})
	go r.loop(ctx)
}

// Stop signals the relay goroutine to shut down and waits for it to finish.
func (r *OutboxRelay) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.cancel()
	r.running = false
	done := r.done
	r.mu.Unlock()
	<-done
}

func (r *OutboxRelay) loop(ctx context.Context) {
	defer close(r.done)
	backoff := r.pollInterval

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		forwarded, err := r.drainAndForward(ctx)
		if err != nil {
			r.logger.Warn("outbox relay error",
				"error", err,
				"backoff", backoff.String(),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*backoffMultiplier, maxBackoff)
			continue
		}

		// Reset backoff on success.
		backoff = r.pollInterval

		if forwarded == 0 {
			// Nothing to process — wait before polling again.
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.pollInterval):
			}
		}
	}
}

func (r *OutboxRelay) drainAndForward(ctx context.Context) (int64, error) {
	events, err := r.outbox.Drain(ctx, r.batchSize)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	for _, evt := range events {
		if err := r.downstream.Publish(ctx, evt); err != nil {
			return 0, err
		}
	}

	n := int64(len(events))
	if err := r.outbox.Ack(ctx, n); err != nil {
		r.logger.Error("outbox ack failed after successful forward",
			"count", n,
			"error", err,
		)
		return 0, err
	}

	r.logger.Debug("outbox relay forwarded events", "count", n)
	return n, nil
}
