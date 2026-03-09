//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
)

func TestSession_CreateAndClose(t *testing.T) {
	sr := createSession(t, map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-1",
			"actor_id":  "actor-1",
			"roles":     []string{"developer"},
		},
	})
	if sr.Session.WorkspacePath == "" {
		t.Fatal("workspace_path should not be empty")
	}
	if sr.Session.CreatedAt == "" {
		t.Fatal("created_at should not be empty")
	}
	if sr.Session.ExpiresAt == "" {
		t.Fatal("expires_at should not be empty")
	}
	closeSession(t, sr.Session.ID)
}

func TestSession_CreateWithMetadata(t *testing.T) {
	sr := createSession(t, map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-meta",
			"actor_id":  "actor-meta",
		},
		"metadata": map[string]string{
			"project": "test-project",
			"env":     "integration",
		},
	})
	defer closeSession(t, sr.Session.ID)

	if sr.Session.Metadata["project"] != "test-project" {
		t.Fatalf("expected metadata project=test-project, got %v", sr.Session.Metadata)
	}
	if sr.Session.Metadata["env"] != "integration" {
		t.Fatalf("expected metadata env=integration, got %v", sr.Session.Metadata)
	}
}

func TestSession_CloseIdempotent(t *testing.T) {
	// Closing a nonexistent session is idempotent — returns 200
	resp := doJSON(t, http.MethodDelete, "/v1/sessions/nonexistent-session-id", nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestSession_CreateMethodNotAllowed(t *testing.T) {
	resp := doGet(t, "/v1/sessions")
	expectStatus(t, resp, http.StatusMethodNotAllowed)
	resp.Body.Close()
}

func TestSession_MultipleIndependent(t *testing.T) {
	s1 := createSession(t, map[string]any{
		"principal": map[string]any{"tenant_id": "t1", "actor_id": "a1"},
	})
	s2 := createSession(t, map[string]any{
		"principal": map[string]any{"tenant_id": "t2", "actor_id": "a2"},
	})
	defer closeSession(t, s1.Session.ID)
	defer closeSession(t, s2.Session.ID)

	if s1.Session.ID == s2.Session.ID {
		t.Fatal("two sessions should have different IDs")
	}
	if s1.Session.WorkspacePath == s2.Session.WorkspacePath {
		t.Fatal("two sessions should have different workspace paths")
	}
}

func TestSession_DoubleCloseIdempotent(t *testing.T) {
	sr := createSession(t, map[string]any{
		"principal": map[string]any{"tenant_id": "t", "actor_id": "a"},
	})
	closeSession(t, sr.Session.ID)

	// Second close is idempotent — returns 200
	resp := doJSON(t, http.MethodDelete, fmt.Sprintf("/v1/sessions/%s", sr.Session.ID), nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestSession_CreateWithExplicitID(t *testing.T) {
	sr := createSession(t, map[string]any{
		"session_id": "explicit-test-session",
		"principal":  map[string]any{"tenant_id": "t", "actor_id": "a"},
	})
	defer closeSession(t, sr.Session.ID)

	if sr.Session.ID != "explicit-test-session" {
		t.Fatalf("expected session ID 'explicit-test-session', got '%s'", sr.Session.ID)
	}
}
