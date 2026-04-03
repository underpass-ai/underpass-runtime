package alertrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePublisher struct {
	events []struct {
		subject string
		data    []byte
	}
	err error
}

func (f *fakePublisher) Publish(_ context.Context, subject string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, struct {
		subject string
		data    []byte
	}{subject, data})
	return nil
}

func firingPayload() GrafanaWebhookPayload {
	return GrafanaWebhookPayload{
		Receiver: "nats-relay",
		Status:   "firing",
		Alerts: []GrafanaAlert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "WorkspaceInvocationFailureRateHigh",
					"severity":  "warning",
					"tool":      "fs.write",
				},
				Annotations: map[string]string{
					"summary":     "Invocation failure rate >5%",
					"description": "fs.write failing at 12% over 10m",
				},
				StartsAt:     "2026-04-03T14:00:00Z",
				Fingerprint:  "abc123",
				Values:       map[string]float64{"failure_rate": 0.12},
				DashboardURL: "https://grafana.underpassai.com/d/underpass-runtime",
			},
		},
		ExternalURL: "https://grafana.underpassai.com",
	}
}

func TestHandler_FiringAlert(t *testing.T) {
	pub := &fakePublisher{}
	h := NewHandler(HandlerConfig{Publisher: pub, Logger: testLogger()})

	body, _ := json.Marshal(firingPayload())
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(pub.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.events))
	}
	if pub.events[0].subject != "observability.alert.firing" {
		t.Fatalf("expected subject observability.alert.firing, got %s", pub.events[0].subject)
	}

	var evt AlertEvent
	if err := json.Unmarshal(pub.events[0].data, &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.AlertName != "WorkspaceInvocationFailureRateHigh" {
		t.Fatalf("expected alert name, got %s", evt.AlertName)
	}
	if evt.Severity != "warning" {
		t.Fatalf("expected warning, got %s", evt.Severity)
	}
	if evt.Type != EventAlertFired {
		t.Fatalf("expected %s, got %s", EventAlertFired, evt.Type)
	}
	if evt.Values["failure_rate"] != 0.12 {
		t.Fatalf("expected failure_rate 0.12, got %v", evt.Values)
	}
}

func TestHandler_ResolvedAlert(t *testing.T) {
	pub := &fakePublisher{}
	h := NewHandler(HandlerConfig{Publisher: pub, Logger: testLogger()})

	payload := firingPayload()
	payload.Alerts[0].Status = "resolved"
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if pub.events[0].subject != "observability.alert.resolved" {
		t.Fatalf("expected resolved subject, got %s", pub.events[0].subject)
	}
}

func TestHandler_AuthToken(t *testing.T) {
	pub := &fakePublisher{}
	h := NewHandler(HandlerConfig{Publisher: pub, AuthToken: "secret-123", Logger: testLogger()})

	body, _ := json.Marshal(firingPayload())

	// No token → 401
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Wrong token → 401
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Correct token → 200
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-123")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := NewHandler(HandlerConfig{Publisher: &fakePublisher{}, Logger: testLogger()})
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandler_InvalidPayload(t *testing.T) {
	h := NewHandler(HandlerConfig{Publisher: &fakePublisher{}, Logger: testLogger()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_PublishError(t *testing.T) {
	pub := &fakePublisher{err: fmt.Errorf("nats down")}
	h := NewHandler(HandlerConfig{Publisher: pub, Logger: testLogger()})

	body, _ := json.Marshal(firingPayload())
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Still returns 200 (best-effort publish)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even on publish error, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["published"].(float64) != 0 {
		t.Fatal("expected 0 published on error")
	}
}

func TestHandler_MultipleAlerts(t *testing.T) {
	pub := &fakePublisher{}
	h := NewHandler(HandlerConfig{Publisher: pub, Logger: testLogger()})

	payload := firingPayload()
	payload.Alerts = append(payload.Alerts, GrafanaAlert{
		Status: "resolved",
		Labels: map[string]string{"alertname": "WorkspaceDown", "severity": "critical"},
		Values: map[string]float64{},
	})
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if len(pub.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(pub.events))
	}
	if pub.events[0].subject != "observability.alert.firing" {
		t.Fatalf("first event should be firing, got %s", pub.events[0].subject)
	}
	if pub.events[1].subject != "observability.alert.resolved" {
		t.Fatalf("second event should be resolved, got %s", pub.events[1].subject)
	}
}

func TestNATSPublisher(t *testing.T) {
	fake := &fakeNATSConn{}
	pub := NewNATSPublisher(fake)
	err := pub.Publish(context.Background(), "test.subject", []byte("data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.subject != "test.subject" {
		t.Fatalf("expected test.subject, got %s", fake.subject)
	}
}

func TestNATSPublisher_Error(t *testing.T) {
	fake := &fakeNATSConn{err: fmt.Errorf("connection refused")}
	pub := NewNATSPublisher(fake)
	err := pub.Publish(context.Background(), "test.subject", []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeNATSConn struct {
	subject string
	data    []byte
	err     error
}

func (f *fakeNATSConn) Publish(subject string, data []byte) error {
	f.subject = subject
	f.data = data
	return f.err
}

func testLogger() *slog.Logger {
	return slog.Default()
}
