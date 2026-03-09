//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	resp := doGet(t, "/healthz")
	expectStatus(t, resp, http.StatusOK)
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

func TestMetrics(t *testing.T) {
	resp := doGet(t, "/metrics")
	expectStatus(t, resp, http.StatusOK)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %s", ct)
	}
	body := string(readBody(t, resp))
	if !strings.Contains(body, "# TYPE") {
		t.Fatalf("expected prometheus TYPE declarations in metrics:\n%s", body)
	}
}

func TestMetrics_MethodNotAllowed(t *testing.T) {
	resp := doJSON(t, http.MethodPost, "/metrics", nil)
	expectStatus(t, resp, http.StatusMethodNotAllowed)
	resp.Body.Close()
}
