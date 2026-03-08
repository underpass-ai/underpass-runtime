package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// --- Use Case: Session Lifecycle over HTTP (create → invoke → close) ---

func TestHTTPIntegration_SessionLifecycle(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// Create session
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-lifecycle",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	session := createBody[testHTTPKeySession].(map[string]any)
	sessionID := session[testHTTPKeyID].(string)

	// List tools
	toolsResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+testPathSessionTools, nil)
	if toolsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 tools, got %d body=%s", toolsResp.StatusCode, toolsResp.Body.String())
	}
	var toolsBody map[string]any
	mustDecode(t, toolsResp.Body.Bytes(), &toolsBody)
	tools := toolsBody["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	// Invoke a tool
	invokeResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
		"args": map[string]any{"path": testSeedFile},
	})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}

	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocation := invokeBody[testHTTPKeyInvocation].(map[string]any)
	if invocation["status"] != "succeeded" {
		t.Fatalf("expected succeeded, got %v", invocation["status"])
	}

	// Close session
	closeResp := doJSONRequest(t, handler, http.MethodDelete, testPathSessionsPrefix+sessionID, nil)
	if closeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 close, got %d body=%s", closeResp.StatusCode, closeResp.Body.String())
	}

	// After close, listing tools should fail
	afterCloseResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+testPathSessionTools, nil)
	if afterCloseResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after session close, got %d", afterCloseResp.StatusCode)
	}
}

// --- Use Case: Discovery → Recommendations → Invoke flow (LLM agent pattern) ---

func TestHTTPIntegration_DiscoverRecommendInvoke(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// Create session
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-dri",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// 1. Discover tools (compact) for read-only tools
	discoverResp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/discovery?risk=low&side_effects=none", nil)
	if discoverResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", discoverResp.StatusCode)
	}
	var discBody map[string]any
	mustDecode(t, discoverResp.Body.Bytes(), &discBody)
	discTools := discBody["tools"].([]any)
	if len(discTools) == 0 {
		t.Fatal("expected at least one low-risk read-only tool")
	}
	// All returned tools should be low risk and no side effects
	for _, raw := range discTools {
		tool := raw.(map[string]any)
		if tool["risk"] != "low" {
			t.Fatalf("filtered tool has risk=%v", tool["risk"])
		}
		if tool["side_effects"] != "none" {
			t.Fatalf("filtered tool has side_effects=%v", tool["side_effects"])
		}
	}

	// 2. Get recommendations for "read file"
	recResp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/recommendations?task_hint=read+file&top_k=3", nil)
	if recResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recResp.StatusCode, recResp.Body.String())
	}
	var recBody map[string]any
	mustDecode(t, recResp.Body.Bytes(), &recBody)
	recs := recBody["recommendations"].([]any)
	if len(recs) == 0 {
		t.Fatal("expected at least one recommendation")
	}
	if len(recs) > 3 {
		t.Fatalf("expected at most 3 recs, got %d", len(recs))
	}

	// fs.read_file should be among top recommendations
	topRec := recs[0].(map[string]any)
	found := false
	for _, r := range recs {
		rec := r.(map[string]any)
		if rec["name"] == "fs.read_file" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected fs.read_file in top 3, top was: %v", topRec["name"])
	}

	// 3. Invoke the recommended tool
	invokeResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
			"args": map[string]any{"path": testSeedFile},
		})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}
}

// --- Use Case: Invocation → Get → Logs → Artifacts retrieval chain ---

func TestHTTPIntegration_InvocationRetrievalChain(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// Create session
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-chain",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Invoke tool
	invokeResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
			"args": map[string]any{"path": testSeedFile},
		})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d", invokeResp.StatusCode)
	}
	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocationID := invokeBody[testHTTPKeyInvocation].(map[string]any)[testHTTPKeyID].(string)

	// Get invocation
	getResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 get, got %d", getResp.StatusCode)
	}
	var getBody map[string]any
	mustDecode(t, getResp.Body.Bytes(), &getBody)
	inv := getBody[testHTTPKeyInvocation].(map[string]any)
	if inv["status"] != "succeeded" {
		t.Fatalf("expected succeeded, got %v", inv["status"])
	}
	if inv["output"] == nil {
		t.Fatal("expected hydrated output")
	}

	// Get logs
	logsResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID+"/logs", nil)
	if logsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 logs, got %d", logsResp.StatusCode)
	}

	// Get artifacts
	artsResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID+"/artifacts", nil)
	if artsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 artifacts, got %d", artsResp.StatusCode)
	}
	var artsBody map[string]any
	mustDecode(t, artsResp.Body.Bytes(), &artsBody)
	artifacts := artsBody["artifacts"].([]any)
	if len(artifacts) == 0 {
		t.Fatal("expected at least one artifact")
	}
}

// --- Use Case: Path Traversal Denied over HTTP ---

func TestHTTPIntegration_PathTraversalDenied(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	// Create session
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-traversal",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Attempt path traversal
	traversalResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
			"args": map[string]any{"path": "../../../etc/passwd"},
		})

	// Should be denied (428 Precondition Required for policy denied)
	if traversalResp.StatusCode == http.StatusOK {
		t.Fatal("expected path traversal to be denied")
	}
	// Policy denial returns 428 or 403 depending on error code
	var body map[string]any
	mustDecode(t, traversalResp.Body.Bytes(), &body)
	if errObj, ok := body["error"].(map[string]any); ok {
		code := errObj["code"].(string)
		if code != "policy_denied" {
			t.Fatalf("expected policy_denied, got %s", code)
		}
	}
}

// --- Use Case: KPI Metrics endpoint ---

func TestHTTPIntegration_KPIMetricsEndpoint(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	// /metrics should return prometheus text
	metricsResp := doJSONRequest(t, handler, http.MethodGet, testPathMetrics, nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", metricsResp.StatusCode)
	}
	body := metricsResp.Body.String()
	if !strings.Contains(body, "invocations_total") {
		t.Fatalf("expected invocations_total in metrics, got: %s", body)
	}
}

// --- Use Case: Write with approval over HTTP → read back via invocation retrieval ---

func TestHTTPIntegration_WriteApprovalAndReadBack(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	// Create session
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-write-read",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Write without approval → 428
	noApprovalResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+"/tools/fs.write_file/invoke", map[string]any{
			"args": map[string]any{"path": "test.txt", "content": "hello"},
		})
	if noApprovalResp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 without approval, got %d", noApprovalResp.StatusCode)
	}

	// Write with approval → 200
	writeResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+"/tools/fs.write_file/invoke", map[string]any{
			"approved": true,
			"args":     map[string]any{"path": "test.txt", "content": "approved content"},
		})
	if writeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with approval, got %d body=%s", writeResp.StatusCode, writeResp.Body.String())
	}

	// Read back
	readResp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
			"args": map[string]any{"path": "test.txt"},
		})
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 read, got %d body=%s", readResp.StatusCode, readResp.Body.String())
	}
	var readBody map[string]any
	mustDecode(t, readResp.Body.Bytes(), &readBody)
	output := readBody[testHTTPKeyInvocation].(map[string]any)["output"].(map[string]any)
	if output["content"] != "approved content" {
		t.Fatalf("expected 'approved content', got %v", output["content"])
	}
}

// --- Use Case: Full discovery → filter by cost ---

func TestHTTPIntegration_DiscoveryFilterByCost(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-cost",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Filter by cost=low (catalog uses cost_hint: low/medium/high)
	lowCostResp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/discovery?cost=low", nil)
	if lowCostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", lowCostResp.StatusCode)
	}
	var lowBody map[string]any
	mustDecode(t, lowCostResp.Body.Bytes(), &lowBody)
	lowTools := lowBody["tools"].([]any)
	if len(lowTools) == 0 {
		t.Fatal("expected at least one low-cost tool")
	}
	for _, raw := range lowTools {
		tool := raw.(map[string]any)
		if tool["cost"] != "low" {
			t.Fatalf("expected cost=low, got %v for %v", tool["cost"], tool["name"])
		}
	}

	// Unfiltered should have more
	allResp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/discovery", nil)
	var allBody map[string]any
	mustDecode(t, allResp.Body.Bytes(), &allBody)
	allTools := allBody["tools"].([]any)
	if len(lowTools) >= len(allTools) {
		t.Fatal("cost=low should return fewer tools than unfiltered")
	}
}

// --- Use Case: Invalid tool invocation (tool not found) ---

func TestHTTPIntegration_InvokeNonexistentTool(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-notfound",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Invoke non-existent tool → 404
	resp := doJSONRequest(t, handler, http.MethodPost,
		testPathSessionsPrefix+sessionID+"/tools/nonexistent.tool/invoke", map[string]any{
			"args": map[string]any{},
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, resp.Body.String())
	}
}
