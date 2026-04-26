package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestNotifyEscalationChannelHandler_Success(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("X-Request-Id", "req-123")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
		"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: server.URL},
	}, server.Client())
	handler.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	result, err := handler.Invoke(context.Background(), session, mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	}))
	if err != nil {
		t.Fatalf("unexpected notify error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["delivered"] != true {
		t.Fatalf("expected delivered=true, got %#v", output)
	}
	if output["channel"] != "#incidents-prod" {
		t.Fatalf("unexpected channel: %#v", output)
	}
	if output["provider_msg_id"] != "req-123" {
		t.Fatalf("unexpected provider_msg_id: %#v", output)
	}
	if captured["incident_id"] != "inc-42" {
		t.Fatalf("unexpected webhook payload: %#v", captured)
	}
}

func TestNotifyEscalationChannelHandler_RateLimited(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
		"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: server.URL},
	}, server.Client())
	now := time.Unix(1_700_000_000, 0).UTC()
	handler.now = func() time.Time { return now }

	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	args := mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	})
	if _, err := handler.Invoke(context.Background(), session, args); err != nil {
		t.Fatalf("unexpected first notify error: %#v", err)
	}
	_, err := handler.Invoke(context.Background(), session, args)
	if err == nil || err.Code != app.ErrorCodePolicyDenied || err.Message != "rate_limit_exceeded" {
		t.Fatalf("expected rate-limit denial, got %#v", err)
	}
	if requests != 1 {
		t.Fatalf("expected exactly one webhook delivery, got %d", requests)
	}
}

func TestNotifyEscalationChannelHandler_MissingRoute(t *testing.T) {
	handler := NewNotifyEscalationChannelHandler(nil, nil)
	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	_, err := handler.Invoke(context.Background(), session, mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected missing-route error, got %#v", err)
	}
}

func TestNotifyEscalationChannelHandlerFromEnv_InvalidConfig(t *testing.T) {
	t.Setenv(notifyEscalationRoutesEnv, `{"prod":`)
	handler := NewNotifyEscalationChannelHandlerFromEnv()
	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	_, err := handler.Invoke(context.Background(), session, mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected config error, got %#v", err)
	}
}

func TestNotifyEscalationChannelHandler_Name(t *testing.T) {
	if NewNotifyEscalationChannelHandler(nil, nil).Name() != "notify.escalation_channel" {
		t.Fatal("expected notify.escalation_channel")
	}
}

func mustNotifyJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal notify args: %v", err)
	}
	return data
}
