package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newNotifyTestClient(fn roundTripperFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func TestNotifyEscalationChannelHandler_Success(t *testing.T) {
	var captured map[string]any
	var contentType string
	client := newNotifyTestClient(func(req *http.Request) (*http.Response, error) {
		contentType = req.Header.Get("Content-Type")
		if req.URL.String() != "https://notify.example.test/escalate" {
			t.Fatalf("unexpected webhook URL: %s", req.URL.String())
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"X-Request-Id": []string{"req-123"},
			},
			Body: io.NopCloser(strings.NewReader("ok")),
		}, nil
	})

	handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
		"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: "https://notify.example.test/escalate"},
	}, client)
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
	if contentType != "application/json" {
		t.Fatalf("unexpected content type: %q", contentType)
	}
	if captured["incident_id"] != "inc-42" {
		t.Fatalf("unexpected webhook payload: %#v", captured)
	}
}

func TestNotifyEscalationChannelHandler_DefaultProviderAndEnvironmentFallback(t *testing.T) {
	client := newNotifyTestClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"X-Correlation-Id": []string{"corr-42"},
			},
			Body: io.NopCloser(strings.NewReader("created")),
		}, nil
	})

	handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
		"e2e": {Channel: "#runtime-e2e", WebhookURL: "http://notify-sink.e2e.svc/notify"},
	}, client)
	session := domain.Session{Metadata: map[string]string{"runtime_environment": "unknown"}}
	t.Setenv("WORKSPACE_ENV", "e2e")

	result, err := handler.Invoke(context.Background(), session, mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-99",
		"handoff_node_id":     "handoff:inc-99:human",
		"summary":             "Need escalation",
		"upstream_specialist": "runtime-saturation-operator",
		"upstream_decision":   "notify",
		"reason":              "manual comms required",
	}))
	if err != nil {
		t.Fatalf("unexpected notify error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["provider"] != notifyDefaultProvider {
		t.Fatalf("expected default provider %q, got %#v", notifyDefaultProvider, output)
	}
	if output["provider_msg_id"] != "corr-42" {
		t.Fatalf("expected fallback provider message ID, got %#v", output)
	}
}

func TestNotifyEscalationChannelHandler_RateLimited(t *testing.T) {
	requests := 0
	client := newNotifyTestClient(func(_ *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	})

	handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
		"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: "https://notify.example.test/escalate"},
	}, client)
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

func TestNotifyEscalationChannelHandler_ValidationAndRoutingErrors(t *testing.T) {
	handler := NewNotifyEscalationChannelHandler(nil, nil)
	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}

	cases := []struct {
		name    string
		args    json.RawMessage
		code    string
		message string
	}{
		{
			name:    "invalid_json",
			args:    json.RawMessage(`{"incident_id":`),
			code:    app.ErrorCodeInvalidArgument,
			message: "invalid notify.escalation_channel args",
		},
		{
			name:    "missing_incident_id",
			args:    mustNotifyJSON(t, map[string]any{"handoff_node_id": "h", "summary": "s", "upstream_specialist": "u", "upstream_decision": "d", "reason": "r"}),
			code:    app.ErrorCodeInvalidArgument,
			message: "incident_id is required",
		},
		{
			name:    "missing_summary",
			args:    mustNotifyJSON(t, map[string]any{"incident_id": "inc-42", "handoff_node_id": "h", "upstream_specialist": "u", "upstream_decision": "d", "reason": "r"}),
			code:    app.ErrorCodeInvalidArgument,
			message: "summary is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Invoke(context.Background(), session, tc.args)
			if err == nil || err.Code != tc.code || err.Message != tc.message {
				t.Fatalf("expected %s/%s, got %#v", tc.code, tc.message, err)
			}
		})
	}
}

func TestNotifyEscalationChannelHandler_MissingRouteAndWebhook(t *testing.T) {
	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	args := mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	})

	t.Run("missing_route", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(nil, nil)
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Code != app.ErrorCodeExecutionFailed {
			t.Fatalf("expected missing-route error, got %#v", err)
		}
	})

	t.Run("missing_webhook", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
			"prod": {Channel: "#incidents-prod", Provider: "slack"},
		}, nil)
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Message != `notify route for environment "prod" is missing webhook_url` {
			t.Fatalf("expected missing webhook error, got %#v", err)
		}
	})
}

func TestNotifyEscalationChannelHandler_DeliveryFailures(t *testing.T) {
	session := domain.Session{Metadata: map[string]string{"environment": "prod"}}
	args := mustNotifyJSON(t, map[string]any{
		"incident_id":         "inc-42",
		"handoff_node_id":     "handoff:inc-42:human",
		"summary":             "Payment needs human review",
		"upstream_specialist": "payment-integrity-operator",
		"upstream_decision":   "escalate",
		"reason":              "callback missing after provider timeout",
	})

	t.Run("client_error_retryable", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
			"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: "https://notify.example.test/escalate"},
		}, newNotifyTestClient(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("dial timeout")
		}))
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Code != app.ErrorCodeExecutionFailed || !err.Retryable {
			t.Fatalf("expected retryable execution failure, got %#v", err)
		}
	})

	t.Run("server_error_retryable", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
			"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: "https://notify.example.test/escalate"},
		}, newNotifyTestClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
			}, nil
		}))
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Code != app.ErrorCodeExecutionFailed || !err.Retryable {
			t.Fatalf("expected retryable HTTP failure, got %#v", err)
		}
	})

	t.Run("client_error_not_retryable", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
			"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: ":://bad-url"},
		}, nil)
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Code != app.ErrorCodeExecutionFailed || err.Retryable {
			t.Fatalf("expected non-retryable request build failure, got %#v", err)
		}
	})
	t.Run("client_error_status_not_retryable", func(t *testing.T) {
		handler := NewNotifyEscalationChannelHandler(map[string]NotifyEscalationRoute{
			"prod": {Channel: "#incidents-prod", Provider: "slack", WebhookURL: "https://notify.example.test/escalate"},
		}, newNotifyTestClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader("slow down")),
			}, nil
		}))
		_, err := handler.Invoke(context.Background(), session, args)
		if err == nil || err.Code != app.ErrorCodeExecutionFailed || err.Retryable {
			t.Fatalf("expected non-retryable client HTTP failure, got %#v", err)
		}
	})
}

func TestNotifyEscalationHelpers(t *testing.T) {
	t.Run("load_routes_blank", func(t *testing.T) {
		routes, err := loadNotifyEscalationRoutes("")
		if err != nil || routes != nil {
			t.Fatalf("expected blank routes to return nil,nil, got routes=%#v err=%v", routes, err)
		}
	})

	t.Run("load_routes_valid", func(t *testing.T) {
		routes, err := loadNotifyEscalationRoutes(`{"prod":{"channel":"#incidents","provider":"slack","webhook_url":"https://notify.example.test"}}`)
		if err != nil {
			t.Fatalf("unexpected routes error: %v", err)
		}
		if routes["prod"].Channel != "#incidents" {
			t.Fatalf("unexpected routes payload: %#v", routes)
		}
	})

	t.Run("runtime_environment_metadata", func(t *testing.T) {
		if got := toolRuntimeEnvironment(map[string]string{"runtime_environment": " staging "}); got != "staging" {
			t.Fatalf("expected runtime_environment fallback, got %q", got)
		}
	})

	t.Run("first_non_empty", func(t *testing.T) {
		if got := firstNonEmptyString("", " ", "x"); got != "x" {
			t.Fatalf("unexpected firstNonEmptyString result: %q", got)
		}
	})
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
