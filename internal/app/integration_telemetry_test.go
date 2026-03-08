package app_test

import (
	"context"
	"strings"
	"testing"

	telemetryadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/telemetry"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// --- Use Case: Telemetry Recording During Invocations ---

func TestIntegration_TelemetryRecording(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Invoke fs.list three times → telemetry should record 3 entries
	for range 3 {
		_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
			Args: mustJSON(t, map[string]any{"path": "."}),
		})
		if invokeErr != nil {
			t.Fatalf("invoke fs.list: %v", invokeErr)
		}
	}

	// Query telemetry stats
	stats, found, statsErr := mem.ToolStats(ctx, "fs.list")
	if statsErr != nil {
		t.Fatalf("query tool stats: %v", statsErr)
	}
	if !found {
		t.Fatal("expected telemetry stats for fs.list")
	}
	if stats.InvocationN != 3 {
		t.Fatalf("expected 3 invocations, got %d", stats.InvocationN)
	}
	if stats.SuccessRate != 1.0 {
		t.Fatalf("expected 100%% success rate, got %v", stats.SuccessRate)
	}
	if stats.DenyRate != 0 {
		t.Fatalf("expected 0%% deny rate, got %v", stats.DenyRate)
	}
}

// --- Use Case: Authorization-denied invocations do NOT record telemetry ---
// Telemetry is only recorded for invocations that reach the execution phase.
// Denials at the authorization layer (policy, approval, rate-limit) are tracked
// by Prometheus metrics, not by the telemetry recorder.

func TestIntegration_TelemetryNotRecorded_ForAuthDenial(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Path traversal → denied by policy (auth layer)
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "../etc/passwd"}),
	})
	if invokeErr == nil {
		t.Fatal("expected denial")
	}

	// Telemetry should NOT record auth-layer denials
	_, found, statsErr := mem.ToolStats(ctx, "fs.read_file")
	if statsErr != nil {
		t.Fatalf("query tool stats: %v", statsErr)
	}
	if found {
		t.Fatal("expected no telemetry record for auth-denied invocation")
	}

	// But a successful invocation SHOULD be recorded
	_, listErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if listErr != nil {
		t.Fatalf("invoke fs.list: %v", listErr)
	}
	listStats, listFound, listStatsErr := mem.ToolStats(ctx, "fs.list")
	if listStatsErr != nil {
		t.Fatalf("query tool stats: %v", listStatsErr)
	}
	if !listFound {
		t.Fatal("expected telemetry record for successful invocation")
	}
	if listStats.InvocationN != 1 {
		t.Fatalf("expected 1 invocation, got %d", listStats.InvocationN)
	}
}

// --- Use Case: Telemetry-Enhanced Full Discovery (stats block populated) ---

func TestIntegration_FullDiscovery_WithTelemetryStats(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Generate telemetry data for fs.list
	for range 5 {
		_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
			Args: mustJSON(t, map[string]any{"path": "."}),
		})
		if invokeErr != nil {
			t.Fatalf("invoke fs.list: %v", invokeErr)
		}
	}

	// Full discovery should include stats for fs.list
	resp, discErr := svc.DiscoverTools(ctx, session.ID, app.DiscoveryDetailFull, app.DiscoveryFilter{})
	if discErr != nil {
		t.Fatalf("discover tools: %v", discErr)
	}

	tools := fullTools(t, resp)
	var fsList *app.FullTool
	for i := range tools {
		if tools[i].Name == "fs.list" {
			fsList = &tools[i]
			break
		}
	}
	if fsList == nil {
		t.Fatal("expected fs.list in discovery response")
	}
	if fsList.Stats == nil {
		t.Fatal("expected stats block for fs.list with telemetry data")
	}
	if fsList.Stats.InvocationN != 5 {
		t.Fatalf("expected 5 invocations in stats, got %d", fsList.Stats.InvocationN)
	}
	if fsList.Stats.SuccessRate != 1.0 {
		t.Fatalf("expected 100%% success rate in stats, got %v", fsList.Stats.SuccessRate)
	}
}

// --- Use Case: Compact Discovery does NOT include stats ---

func TestIntegration_CompactDiscovery_NoStats(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Generate telemetry
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if invokeErr != nil {
		t.Fatalf("invoke: %v", invokeErr)
	}

	// Compact view should not have stats field
	resp, discErr := svc.DiscoverTools(ctx, session.ID, app.DiscoveryDetailCompact, app.DiscoveryFilter{})
	if discErr != nil {
		t.Fatalf("discover: %v", discErr)
	}
	tools := compactTools(t, resp)
	if len(tools) == 0 {
		t.Fatal("expected tools")
	}
	// CompactTool struct does not have Stats field — this is a compile-time guarantee.
	// Just verify the response works as compact.
	for _, ct := range tools {
		if ct.Name == "" {
			t.Fatal("expected non-empty name in compact tool")
		}
	}
}

// --- Use Case: Full Discovery without telemetry → stats=nil ---

func TestIntegration_FullDiscovery_WithoutTelemetry(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	resp, discErr := svc.DiscoverTools(ctx, session.ID, app.DiscoveryDetailFull, app.DiscoveryFilter{})
	if discErr != nil {
		t.Fatalf("discover: %v", discErr)
	}

	tools := fullTools(t, resp)
	for _, ft := range tools {
		if ft.Stats != nil {
			t.Fatalf("expected nil stats without telemetry data for %s", ft.Name)
		}
	}
}

// --- Use Case: Telemetry-Enhanced Recommendations ---

func TestIntegration_Recommendations_WithTelemetryBoost(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Generate telemetry: 6 successful invocations of fs.list (above telSuccessMinN=5 threshold)
	for range 6 {
		_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
			Args: mustJSON(t, map[string]any{"path": "."}),
		})
		if invokeErr != nil {
			t.Fatalf("invoke: %v", invokeErr)
		}
	}

	// Get recommendations without telemetry for baseline
	svcBaseline := setupService(t)
	sessionBaseline, baseErr := svcBaseline.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if baseErr != nil {
		t.Fatalf("create baseline session: %v", baseErr)
	}

	baselineResp, baseRecErr := svcBaseline.RecommendTools(ctx, sessionBaseline.ID, "", 0)
	if baseRecErr != nil {
		t.Fatalf("baseline recommendations: %v", baseRecErr)
	}

	// Get recommendations with telemetry
	boostedResp, recErr := svc.RecommendTools(ctx, session.ID, "", 0)
	if recErr != nil {
		t.Fatalf("boosted recommendations: %v", recErr)
	}

	// Find fs.list score in both
	var baseScore, boostedScore float64
	for _, r := range baselineResp.Recommendations {
		if r.Name == "fs.list" {
			baseScore = r.Score
			break
		}
	}
	for _, r := range boostedResp.Recommendations {
		if r.Name == "fs.list" {
			boostedScore = r.Score
			break
		}
	}

	if boostedScore <= baseScore {
		t.Fatalf("expected telemetry boost to increase fs.list score: baseline=%v boosted=%v", baseScore, boostedScore)
	}
}

// --- Use Case: KPI Metrics Flow ---

func TestIntegration_KPIMetrics(t *testing.T) {
	svc := setupService(t)
	kpi := app.NewKPIMetrics()
	svc.SetKPIMetrics(kpi)

	// Observe KPI events
	kpi.ObserveToolCall("fs.list")
	kpi.ObserveToolCall("fs.list")
	kpi.ObserveToolCall("fs.read_file")
	kpi.ObserveFirstToolResult(true)
	kpi.ObserveFirstToolResult(false)
	kpi.ObserveRecommendationUsed(true)
	kpi.ObservePolicyDenialAfterRecommendation(false)
	kpi.ObserveContextBytesSaved(2048)

	// Retrieve KPI metrics via service
	text := svc.KPIPrometheusMetrics()
	if text == "" {
		t.Fatal("expected non-empty KPI prometheus metrics")
	}

	expectedMetrics := []string{
		`workspace_tool_calls_per_task{task="fs.list"} 2`,
		`workspace_tool_calls_per_task{task="fs.read_file"} 1`,
		"workspace_success_on_first_tool_total 2",
		"workspace_success_on_first_tool_rate 0.5",
		"workspace_recommendation_total 1",
		"workspace_recommendation_acceptance_rate 1.0",
		"workspace_context_bytes_saved 2048",
	}
	for _, expected := range expectedMetrics {
		if !strings.Contains(text, expected) {
			t.Errorf("missing metric %q in KPI output:\n%s", expected, text)
		}
	}

	// Verify GetKPIMetrics returns the same instance
	retrieved := svc.GetKPIMetrics()
	if retrieved != kpi {
		t.Fatal("expected GetKPIMetrics to return same instance")
	}
}

// --- Use Case: Telemetry records multiple successful tools ---

func TestIntegration_TelemetryMultipleTools(t *testing.T) {
	svc := setupService(t)
	mem := telemetryadapter.NewInMemoryAggregator()
	svc.SetTelemetry(mem, mem)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Successful list
	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.list", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "."}),
	})
	if invokeErr != nil {
		t.Fatalf("invoke fs.list: %v", invokeErr)
	}

	// Successful write + read (both reach execution phase)
	_, writeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Approved: true,
		Args:     mustJSON(t, map[string]any{"path": "tel.txt", "content": "telemetry test"}),
	})
	if writeErr != nil {
		t.Fatalf("invoke fs.write_file: %v", writeErr)
	}

	_, readErr := svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "tel.txt"}),
	})
	if readErr != nil {
		t.Fatalf("invoke fs.read_file: %v", readErr)
	}

	// AllToolStats should have entries for all 3 tools
	allStats, allStatsErr := mem.AllToolStats(ctx)
	if allStatsErr != nil {
		t.Fatalf("all tool stats: %v", allStatsErr)
	}

	if len(allStats) < 3 {
		t.Fatalf("expected stats for at least 3 tools, got %d", len(allStats))
	}

	// All should be 100% success
	for name, stats := range allStats {
		if stats.SuccessRate != 1.0 {
			t.Fatalf("expected %s success_rate=1.0, got %v", name, stats.SuccessRate)
		}
		if stats.InvocationN != 1 {
			t.Fatalf("expected %s invocation_count=1, got %d", name, stats.InvocationN)
		}
	}

	// Auth-denied invocations should NOT appear in telemetry
	_, _ = svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "../../etc/shadow"}),
	})
	allStatsAfterDenial, _ := mem.AllToolStats(ctx)
	readStats := allStatsAfterDenial["fs.read_file"]
	if readStats.InvocationN != 1 {
		t.Fatalf("expected fs.read_file invocation_count=1 (denial not recorded), got %d", readStats.InvocationN)
	}
}
