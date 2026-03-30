package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func testMetrics() domain.InvocationQualityMetrics {
	inv := domain.Invocation{
		ID:         "inv-test",
		ToolName:   "fs.write_file",
		Status:     domain.InvocationStatusSucceeded,
		DurationMS: 45,
		ExitCode:   0,
	}
	m, _ := domain.ComputeInvocationQuality(inv)
	return m
}

func testContext() domain.QualityObservationContext {
	return domain.QualityObservationContext{
		SessionID: "session-123",
		TenantID:  "tenant-abc",
		ActorID:   "agent-dev",
		Timestamp: time.Now(),
	}
}

func TestNoopQualityObserver(t *testing.T) {
	obs := NoopQualityObserver{}
	// Should not panic.
	obs.ObserveInvocationQuality(context.Background(), testMetrics(), testContext())
}

func TestSlogQualityObserver(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	obs := NewSlogQualityObserver(logger)
	obs.ObserveInvocationQuality(context.Background(), testMetrics(), testContext())

	output := buf.String()
	for _, expected := range []string{
		"invocation.quality",
		"fs.write_file",
		"succeeded",
		"fast",
		"session-123",
		"tenant-abc",
		"agent-dev",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in log output, got: %s", expected, output)
		}
	}
}

func TestCompositeQualityObserver(t *testing.T) {
	callCount := 0
	tracker := &trackingObserver{onObserve: func() { callCount++ }}
	composite := NewCompositeQualityObserver(tracker, tracker, tracker)
	composite.ObserveInvocationQuality(context.Background(), testMetrics(), testContext())

	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

type trackingObserver struct {
	onObserve func()
}

func (t *trackingObserver) ObserveInvocationQuality(_ context.Context, _ domain.InvocationQualityMetrics, _ domain.QualityObservationContext) {
	t.onObserve()
}
