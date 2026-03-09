//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
)

// runtimeURL returns the base URL for the runtime service.
// Defaults to http://runtime:50053 inside the devcontainer network.
func runtimeURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("RUNTIME_URL"); url != "" {
		return url
	}
	return "http://runtime:50053"
}

// sessionResponse is the shape returned by POST /v1/sessions.
type sessionResponse struct {
	Session struct {
		ID            string            `json:"id"`
		WorkspacePath string            `json:"workspace_path"`
		Principal     principal         `json:"principal"`
		Metadata      map[string]string `json:"metadata,omitempty"`
		CreatedAt     string            `json:"created_at"`
		ExpiresAt     string            `json:"expires_at"`
	} `json:"session"`
}

type principal struct {
	TenantID string   `json:"tenant_id"`
	ActorID  string   `json:"actor_id"`
	Roles    []string `json:"roles"`
}

// invocationResponse is the shape returned by POST .../invoke and GET /v1/invocations/{id}.
type invocationResponse struct {
	Invocation struct {
		ID            string `json:"id"`
		SessionID     string `json:"session_id"`
		ToolName      string `json:"tool_name"`
		CorrelationID string `json:"correlation_id,omitempty"`
		Status        string `json:"status"`
		ExitCode      int    `json:"exit_code"`
		DurationMS    int64  `json:"duration_ms"`
		Output        any    `json:"output,omitempty"`
	} `json:"invocation"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// createSession creates a new session and returns the parsed response.
func createSession(t *testing.T, body map[string]any) sessionResponse {
	t.Helper()
	resp := doJSON(t, http.MethodPost, "/v1/sessions", body)
	expectStatus(t, resp, http.StatusCreated)
	var sr sessionResponse
	decodeJSON(t, resp, &sr)
	if sr.Session.ID == "" {
		t.Fatal("session ID is empty")
	}
	return sr
}

// closeSession closes a session.
func closeSession(t *testing.T, sessionID string) {
	t.Helper()
	resp := doJSON(t, http.MethodDelete, fmt.Sprintf("/v1/sessions/%s", sessionID), nil)
	expectStatus(t, resp, http.StatusOK)
}

// doJSON performs an HTTP request with optional JSON body.
func doJSON(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	url := runtimeURL(t) + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// doGet performs a GET request.
func doGet(t *testing.T, path string) *http.Response {
	t.Helper()
	return doJSON(t, http.MethodGet, path, nil)
}

// expectStatus checks the response status code. On failure, reads body for diagnostics.
func expectStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected status %d, got %d; body: %s", expected, resp.StatusCode, string(body))
	}
}

// decodeJSON reads the response body and unmarshals it into dest.
func decodeJSON(t *testing.T, resp *http.Response, dest any) {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		t.Fatalf("decode JSON: %v; body: %s", err, string(data))
	}
}

// readBody reads and returns the response body bytes.
func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

// defaultPrincipal returns a minimal principal for test sessions.
func defaultPrincipal() map[string]any {
	return map[string]any{
		"tenant_id": "integration-test",
		"actor_id":  "test-actor",
		"roles":     []string{"developer"},
	}
}

// withSession creates a session, runs fn, then closes it.
func withSession(t *testing.T, fn func(sessionID string)) {
	t.Helper()
	sr := createSession(t, map[string]any{"principal": defaultPrincipal()})
	defer closeSession(t, sr.Session.ID)
	fn(sr.Session.ID)
}
