//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestInvoke_FsList(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, resp, http.StatusOK)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Invocation.ID == "" {
			t.Fatal("invocation ID should not be empty")
		}
		if ir.Invocation.SessionID != sid {
			t.Fatalf("expected session_id=%s, got %s", sid, ir.Invocation.SessionID)
		}
		if ir.Invocation.ToolName != "fs.list" {
			t.Fatalf("expected tool_name=fs.list, got %s", ir.Invocation.ToolName)
		}
		if ir.Invocation.Status != "succeeded" {
			t.Fatalf("expected status=succeeded, got %s", ir.Invocation.Status)
		}
	})
}

func TestInvoke_WithCorrelationID(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"correlation_id": "corr-123",
				"args":           json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, resp, http.StatusOK)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Invocation.CorrelationID != "corr-123" {
			t.Fatalf("expected correlation_id=corr-123, got %s", ir.Invocation.CorrelationID)
		}
	})
}

func TestInvoke_ApprovalRequired(t *testing.T) {
	withSession(t, func(sid string) {
		// fs.write_file requires approval — invoke without approved=true
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.write_file/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"test.txt","content":"hello"}`),
			},
		)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Invocation.Status != "denied" {
			t.Fatalf("expected status=denied, got %s", ir.Invocation.Status)
		}
		if ir.Error == nil {
			t.Fatal("expected error in response")
		}
		if ir.Error.Code != "approval_required" {
			t.Fatalf("expected error code=approval_required, got %s", ir.Error.Code)
		}
	})
}

func TestInvoke_WithApproval(t *testing.T) {
	withSession(t, func(sid string) {
		// fs.write_file with approved=true
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.write_file/invoke", sid),
			map[string]any{
				"args":     json.RawMessage(`{"path":"test-write.txt","content":"hello integration"}`),
				"approved": true,
			},
		)
		expectStatus(t, resp, http.StatusOK)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Invocation.Status != "succeeded" {
			t.Fatalf("expected status=succeeded, got %s (error: %+v)", ir.Invocation.Status, ir.Error)
		}

		// Verify the file was written by reading it back
		readResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.read_file/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"test-write.txt"}`),
			},
		)
		expectStatus(t, readResp, http.StatusOK)

		var readIR invocationResponse
		decodeJSON(t, readResp, &readIR)

		if readIR.Invocation.Status != "succeeded" {
			t.Fatalf("read back failed: status=%s", readIR.Invocation.Status)
		}
	})
}

func TestInvoke_ToolNotFound(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/nonexistent.tool/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{}`),
			},
		)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Error == nil {
			t.Fatal("expected error for nonexistent tool")
		}
	})
}

func TestInvoke_SessionNotFound(t *testing.T) {
	resp := doJSON(t, http.MethodPost,
		"/v1/sessions/does-not-exist/tools/fs.list/invoke",
		map[string]any{
			"args": json.RawMessage(`{"path":"."}`),
		},
	)
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestInvoke_MethodNotAllowed(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid))
		expectStatus(t, resp, http.StatusMethodNotAllowed)
		resp.Body.Close()
	})
}

func TestInvoke_PathTraversal(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"../../etc"}`),
			},
		)

		var ir invocationResponse
		decodeJSON(t, resp, &ir)

		if ir.Invocation.Status != "denied" {
			t.Fatalf("expected denied for path traversal, got %s", ir.Invocation.Status)
		}
		if ir.Error == nil {
			t.Fatal("expected error for path traversal")
		}
		if ir.Error.Code != "policy_denied" {
			t.Fatalf("expected error code=policy_denied, got %s", ir.Error.Code)
		}
	})
}
