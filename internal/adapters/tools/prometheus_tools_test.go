package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func newTestPrometheusHandler() *PrometheusQueryHandler {
	return &PrometheusQueryHandler{httpClient: &http.Client{Timeout: 2 * time.Second}}
}

func TestPrometheusQueryHandler_ReachableNonPrometheusEndpointFails(t *testing.T) {
	// httpbin-style endpoint: reachable, 200 OK, but NOT a Prometheus API — its
	// JSON has no {status:"success", data.result} shape. The tool must surface a
	// real failure, not mask it as a benign threshold timeout.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slideshow":{"title":"not prometheus"}}`))
	}))
	defer server.Close()

	handler := newTestPrometheusHandler()
	_, err := handler.Invoke(context.Background(), domain.Session{}, mustJSON(t, map[string]any{
		"query":           "vector(1)",
		"url":             server.URL,
		"timeout_seconds": 1,
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed for a reachable non-prometheus endpoint, got %#v", err)
	}
}

func TestPrometheusQueryHandler_ThresholdMet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"value":[0,"1"]}]}}`))
	}))
	defer server.Close()

	handler := newTestPrometheusHandler()
	result, err := handler.Invoke(context.Background(), domain.Session{}, mustJSON(t, map[string]any{
		"query":           "vector(1)",
		"url":             server.URL,
		"timeout_seconds": 5,
	}))
	if err != nil {
		t.Fatalf("unexpected error for a valid prometheus response: %#v", err)
	}
	out := result.Output.(map[string]any)
	if out["threshold_met"] != true {
		t.Fatalf("expected threshold_met=true, got %#v", out["threshold_met"])
	}
}

func TestPrometheusQueryHandler_ZeroTimeoutReturnsImmediately(t *testing.T) {
	// timeout_seconds:0 -> the wait loop never runs -> a benign not-met/timeout
	// result (never a masked failure, and never a real query attempt).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("prometheus must not be queried when timeout_seconds is 0")
	}))
	defer server.Close()

	handler := newTestPrometheusHandler()
	result, err := handler.Invoke(context.Background(), domain.Session{}, mustJSON(t, map[string]any{
		"query":           "vector(1)",
		"url":             server.URL,
		"timeout_seconds": 0,
	}))
	if err != nil {
		t.Fatalf("expected zero-timeout to return a benign result, got %#v", err)
	}
	out := result.Output.(map[string]any)
	if out["threshold_met"] != false || out["timeout"] != true {
		t.Fatalf("unexpected zero-timeout output: %#v", out)
	}
}
