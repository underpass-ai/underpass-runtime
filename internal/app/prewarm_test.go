package app

import (
	"context"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestPrewarmSession_PopulatesCache(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetTelemetry(noopTelemetryRecorder{}, &fakeTelemetryQuerier{
		stats: map[string]ToolStats{"fs.read_file": {SuccessRate: 0.9, InvocationN: 10}},
	})

	session := domain.Session{
		ID:            "prewarm-test",
		WorkspacePath: "/tmp/ws",
		Principal:     domain.Principal{TenantID: "t1", ActorID: "a1"},
	}
	svc.prewarmSession(session)

	// Wait for background goroutine
	time.Sleep(100 * time.Millisecond)

	_, stats, _, ok := svc.getWarmData("prewarm-test")
	if !ok {
		t.Fatal("expected warm data to be ready")
	}
	if stats == nil {
		t.Fatal("expected stats in warm cache")
	}
	if stats["fs.read_file"].SuccessRate != 0.9 {
		t.Fatalf("expected 0.9 success rate, got %f", stats["fs.read_file"].SuccessRate)
	}
}

func TestPrewarmSession_CacheEvictedOnClose(t *testing.T) {
	svc, _ := makeEvidenceService()

	session := domain.Session{
		ID:            "evict-test",
		WorkspacePath: "/tmp/ws",
		Principal:     domain.Principal{TenantID: "t1", ActorID: "a1"},
	}
	svc.prewarmSession(session)
	time.Sleep(100 * time.Millisecond)

	_, _, _, ok := svc.getWarmData("evict-test")
	if !ok {
		t.Fatal("expected warm data before evict")
	}

	svc.warmCaches.evict("evict-test")

	_, _, _, ok = svc.getWarmData("evict-test")
	if ok {
		t.Fatal("expected warm data evicted")
	}
}

func TestGetWarmData_NotReady(t *testing.T) {
	svc, _ := makeEvidenceService()
	_, _, _, ok := svc.getWarmData("nonexistent")
	if ok {
		t.Fatal("expected not ready for unknown session")
	}
}

func TestRecommendTools_UsesPrewarmedData(t *testing.T) {
	svc, _ := makeEvidenceService()
	svc.SetTelemetry(noopTelemetryRecorder{}, &fakeTelemetryQuerier{
		stats: map[string]ToolStats{"fs.read_file": {SuccessRate: 0.95, InvocationN: 20}},
	})

	// Create session triggers prewarm
	_, svcErr := svc.CreateSession(context.Background(), CreateSessionRequest{
		SessionID:       testSessionID,
		Principal:       domain.Principal{TenantID: "t1", ActorID: "a1"},
		ExpiresInSecond: 60,
	})
	if svcErr != nil {
		t.Fatalf("create session: %v", svcErr)
	}

	// Wait for prewarm
	time.Sleep(200 * time.Millisecond)

	// RecommendTools should use prewarmed data
	resp, svcErr := svc.RecommendTools(context.Background(), testSessionID, "read", 5)
	if svcErr != nil {
		t.Fatalf("recommend: %v", svcErr)
	}
	if len(resp.Recommendations) == 0 {
		t.Fatal("expected recommendations")
	}

	// Verify telemetry boost was applied from warm cache
	hasTelemetry := false
	for _, sc := range resp.Recommendations[0].ScoreBreakdown {
		if sc.Name == "telemetry_boost" {
			hasTelemetry = true
		}
	}
	if !hasTelemetry {
		t.Fatal("expected telemetry boost from prewarmed stats")
	}
}
