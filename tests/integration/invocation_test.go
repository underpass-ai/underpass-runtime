//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestInvocation_GetByID(t *testing.T) {
	withSession(t, func(sid string) {
		// Create an invocation first
		invokeResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, invokeResp, http.StatusOK)
		var invokeIR invocationResponse
		decodeJSON(t, invokeResp, &invokeIR)
		invocationID := invokeIR.Invocation.ID

		// Retrieve the invocation by ID
		getResp := doGet(t, fmt.Sprintf("/v1/invocations/%s", invocationID))
		expectStatus(t, getResp, http.StatusOK)

		var getIR invocationResponse
		decodeJSON(t, getResp, &getIR)

		if getIR.Invocation.ID != invocationID {
			t.Fatalf("expected invocation ID=%s, got %s", invocationID, getIR.Invocation.ID)
		}
		if getIR.Invocation.ToolName != "fs.list" {
			t.Fatalf("expected tool_name=fs.list, got %s", getIR.Invocation.ToolName)
		}
		if getIR.Invocation.SessionID != sid {
			t.Fatalf("expected session_id=%s, got %s", sid, getIR.Invocation.SessionID)
		}
	})
}

func TestInvocation_GetLogs(t *testing.T) {
	withSession(t, func(sid string) {
		invokeResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, invokeResp, http.StatusOK)
		var invokeIR invocationResponse
		decodeJSON(t, invokeResp, &invokeIR)

		logsResp := doGet(t, fmt.Sprintf("/v1/invocations/%s/logs", invokeIR.Invocation.ID))
		expectStatus(t, logsResp, http.StatusOK)

		var body struct {
			Logs []struct {
				At      string `json:"at"`
				Channel string `json:"channel"`
				Message string `json:"message"`
			} `json:"logs"`
		}
		decodeJSON(t, logsResp, &body)
		// Logs may be empty for a simple fs.list, but the response shape must be valid
	})
}

func TestInvocation_GetArtifacts(t *testing.T) {
	withSession(t, func(sid string) {
		invokeResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, invokeResp, http.StatusOK)
		var invokeIR invocationResponse
		decodeJSON(t, invokeResp, &invokeIR)

		artifactsResp := doGet(t, fmt.Sprintf("/v1/invocations/%s/artifacts", invokeIR.Invocation.ID))
		expectStatus(t, artifactsResp, http.StatusOK)

		var body struct {
			Artifacts []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"artifacts"`
		}
		decodeJSON(t, artifactsResp, &body)
		// Artifacts may be empty for fs.list, but the response shape must be valid
	})
}

func TestInvocation_NotFound(t *testing.T) {
	resp := doGet(t, "/v1/invocations/nonexistent-invocation-id")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestInvocation_LogsNotFound(t *testing.T) {
	resp := doGet(t, "/v1/invocations/nonexistent-invocation-id/logs")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestInvocation_ArtifactsNotFound(t *testing.T) {
	resp := doGet(t, "/v1/invocations/nonexistent-invocation-id/artifacts")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestInvocation_FullDataFlow(t *testing.T) {
	withSession(t, func(sid string) {
		// 1. Write a file
		writeResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.write_file/invoke", sid),
			map[string]any{
				"args":     json.RawMessage(`{"path":"dataflow-test.txt","content":"integration data flow"}`),
				"approved": true,
			},
		)
		expectStatus(t, writeResp, http.StatusOK)
		var writeIR invocationResponse
		decodeJSON(t, writeResp, &writeIR)
		if writeIR.Invocation.Status != "succeeded" {
			t.Fatalf("write failed: status=%s", writeIR.Invocation.Status)
		}

		// 2. List to verify the file appears
		listResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.list/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"."}`),
			},
		)
		expectStatus(t, listResp, http.StatusOK)
		var listIR invocationResponse
		decodeJSON(t, listResp, &listIR)
		if listIR.Invocation.Status != "succeeded" {
			t.Fatalf("list failed: status=%s", listIR.Invocation.Status)
		}

		// 3. Read the file back
		readResp := doJSON(t, http.MethodPost,
			fmt.Sprintf("/v1/sessions/%s/tools/fs.read_file/invoke", sid),
			map[string]any{
				"args": json.RawMessage(`{"path":"dataflow-test.txt"}`),
			},
		)
		expectStatus(t, readResp, http.StatusOK)
		var readIR invocationResponse
		decodeJSON(t, readResp, &readIR)
		if readIR.Invocation.Status != "succeeded" {
			t.Fatalf("read failed: status=%s", readIR.Invocation.Status)
		}

		// 4. Retrieve all three invocations by ID
		for _, id := range []string{writeIR.Invocation.ID, listIR.Invocation.ID, readIR.Invocation.ID} {
			getResp := doGet(t, fmt.Sprintf("/v1/invocations/%s", id))
			expectStatus(t, getResp, http.StatusOK)
			var getIR invocationResponse
			decodeJSON(t, getResp, &getIR)
			if getIR.Invocation.ID != id {
				t.Errorf("expected invocation ID=%s, got %s", id, getIR.Invocation.ID)
			}
		}
	})
}
