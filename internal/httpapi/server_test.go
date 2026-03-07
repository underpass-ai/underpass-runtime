package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/adapters/audit"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/policy"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
)

func TestHTTPAPI_EndToEndToolExecutionInWorkspace(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createPayload := map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-a",
			"actor_id":  "agent-1",
			"roles":     []string{"developer"},
		},
		"source_repo_path":   sourcePath,
		"expires_in_seconds": 3600,
	}
	createResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions", createPayload)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	session := createBody["session"].(map[string]any)
	sessionID := session["id"].(string)
	workspacePath := session["workspace_path"].(string)

	invokePayload := map[string]any{
		"approved": true,
		"args": map[string]any{
			"path":           "notes/result.txt",
			"content":        "workspace ok",
			"create_parents": true,
		},
	}
	invokeResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.write_file/invoke", invokePayload)
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}

	if _, err := os.Stat(filepath.Join(workspacePath, "notes", "result.txt")); err != nil {
		t.Fatalf("expected written file in workspace, got error: %v", err)
	}

	readPayload := map[string]any{"args": map[string]any{"path": "notes/result.txt"}}
	readResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.read_file/invoke", readPayload)
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on fs.read_file, got %d body=%s", readResp.StatusCode, readResp.Body.String())
	}

	var readBody map[string]any
	mustDecode(t, readResp.Body.Bytes(), &readBody)
	invocation := readBody["invocation"].(map[string]any)
	output := invocation["output"].(map[string]any)
	if output["content"].(string) != "workspace ok" {
		t.Fatalf("unexpected read content: %#v", output)
	}
}

func TestHTTPAPI_ApprovalRequiredAndRouteErrors(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	createPayload := map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-a",
			"actor_id":  "agent-2",
			"roles":     []string{"developer"},
		},
	}
	createResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions", createPayload)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody["session"].(map[string]any)["id"].(string)

	denyPayload := map[string]any{
		"approved": false,
		"args": map[string]any{
			"path":    "x.txt",
			"content": "denied",
		},
	}
	denyResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.write_file/invoke", denyPayload)
	if denyResp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected 428, got %d body=%s", denyResp.StatusCode, denyResp.Body.String())
	}

	notFoundResp := doJSONRequest(t, handler, http.MethodGet, "/v1/sessions/"+sessionID+"/unknown", nil)
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", notFoundResp.StatusCode)
	}

	methodResp := doJSONRequest(t, handler, http.MethodGet, "/v1/sessions", nil)
	if methodResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", methodResp.StatusCode)
	}
}

func TestHTTPAPI_InvocationRoutesAndHealth(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	healthResp := doJSONRequest(t, handler, http.MethodGet, "/healthz", nil)
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 healthz, got %d", healthResp.StatusCode)
	}
	metricsResp := doJSONRequest(t, handler, http.MethodGet, "/metrics", nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metrics, got %d body=%s", metricsResp.StatusCode, metricsResp.Body.String())
	}
	if !strings.Contains(metricsResp.Body.String(), "invocations_total") {
		t.Fatalf("expected metrics payload, got: %s", metricsResp.Body.String())
	}

	createResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions", map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-a",
			"actor_id":  "agent-3",
			"roles":     []string{"developer"},
		},
		"source_repo_path": sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody["session"].(map[string]any)["id"].(string)

	invokeResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.read_file/invoke", map[string]any{
		"args": map[string]any{"path": "seed.txt"},
	})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}

	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocationID := invokeBody["invocation"].(map[string]any)["id"].(string)

	getResp := doJSONRequest(t, handler, http.MethodGet, "/v1/invocations/"+invocationID, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation get, got %d", getResp.StatusCode)
	}

	logsResp := doJSONRequest(t, handler, http.MethodGet, "/v1/invocations/"+invocationID+"/logs", nil)
	if logsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation logs, got %d", logsResp.StatusCode)
	}

	artifactsResp := doJSONRequest(t, handler, http.MethodGet, "/v1/invocations/"+invocationID+"/artifacts", nil)
	if artifactsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation artifacts, got %d", artifactsResp.StatusCode)
	}

	postInvokeMetrics := doJSONRequest(t, handler, http.MethodGet, "/metrics", nil)
	if postInvokeMetrics.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metrics after invoke, got %d", postInvokeMetrics.StatusCode)
	}
	if !strings.Contains(postInvokeMetrics.Body.String(), `invocations_total{tool="fs.read_file",status="succeeded"} 1`) {
		t.Fatalf("expected fs.read_file succeeded counter in metrics, got: %s", postInvokeMetrics.Body.String())
	}

	malformedResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.read_file/invoke", json.RawMessage(`{"args":`))
	if malformedResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 malformed invoke body, got %d", malformedResp.StatusCode)
	}

	invMethodResp := doJSONRequest(t, handler, http.MethodPost, "/v1/invocations/"+invocationID+"/logs", map[string]any{})
	if invMethodResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 invocation logs method, got %d", invMethodResp.StatusCode)
	}

	missingInvResp := doJSONRequest(t, handler, http.MethodGet, "/v1/invocations/missing", nil)
	if missingInvResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 missing invocation, got %d", missingInvResp.StatusCode)
	}
}

func TestHTTPAPI_TrustedHeadersUsesAuthenticatedPrincipal(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = "workspace-shared-token"

	handler, _ := setupHTTPHandler(t, authCfg)
	headers := map[string]string{
		authCfg.TokenHeader:  "workspace-shared-token",
		authCfg.TenantHeader: "tenant-auth",
		authCfg.ActorHeader:  "actor-auth",
		authCfg.RolesHeader:  "devops,developer",
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/v1/sessions", map[string]any{
		"principal": map[string]any{
			"tenant_id": "payload-tenant",
			"actor_id":  "payload-actor",
			"roles":     []string{"admin"},
		},
	}, headers)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create in trusted mode, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	session := createBody["session"].(map[string]any)
	principal := session["principal"].(map[string]any)
	if principal["tenant_id"] != "tenant-auth" || principal["actor_id"] != "actor-auth" {
		t.Fatalf("expected principal from trusted headers, got %#v", principal)
	}
}

func TestHTTPAPI_TrustedHeadersRejectsMissingToken(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = "workspace-shared-token"

	handler, _ := setupHTTPHandler(t, authCfg)
	headers := map[string]string{
		authCfg.TenantHeader: "tenant-auth",
		authCfg.ActorHeader:  "actor-auth",
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/v1/sessions", map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-auth",
			"actor_id":  "actor-auth",
		},
	}, headers)
	if createResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized without token, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
}

func TestHTTPAPI_TrustedHeadersEnforcesSessionOwnership(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = "workspace-shared-token"

	handler, _ := setupHTTPHandler(t, authCfg)
	ownerHeaders := map[string]string{
		authCfg.TokenHeader:  "workspace-shared-token",
		authCfg.TenantHeader: "tenant-auth",
		authCfg.ActorHeader:  "actor-owner",
		authCfg.RolesHeader:  "developer",
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/v1/sessions", map[string]any{}, ownerHeaders)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody["session"].(map[string]any)["id"].(string)

	ownerListResp := doJSONRequestWithHeaders(t, handler, http.MethodGet, "/v1/sessions/"+sessionID+"/tools", nil, ownerHeaders)
	if ownerListResp.StatusCode != http.StatusOK {
		t.Fatalf("expected owner access to session tools, got %d body=%s", ownerListResp.StatusCode, ownerListResp.Body.String())
	}

	otherHeaders := map[string]string{
		authCfg.TokenHeader:  "workspace-shared-token",
		authCfg.TenantHeader: "tenant-auth",
		authCfg.ActorHeader:  "actor-other",
		authCfg.RolesHeader:  "developer",
	}
	otherListResp := doJSONRequestWithHeaders(t, handler, http.MethodGet, "/v1/sessions/"+sessionID+"/tools", nil, otherHeaders)
	if otherListResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d body=%s", otherListResp.StatusCode, otherListResp.Body.String())
	}
}

func setupHTTPHandler(t *testing.T, authCfg ...AuthConfig) (http.Handler, string) {
	t.Helper()

	workspaceRoot := t.TempDir()
	artifactRoot := t.TempDir()
	sourcePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourcePath, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("write source seed failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	workspaceManager := workspaceadapter.NewLocalManager(workspaceRoot)
	catalog := tooladapter.NewCatalog(tooladapter.DefaultCapabilities())
	commandRunner := tooladapter.NewLocalCommandRunner()
	engine := tooladapter.NewEngine(
		tooladapter.NewFSListHandler(commandRunner),
		tooladapter.NewFSReadHandler(commandRunner),
		tooladapter.NewFSWriteHandler(commandRunner),
		tooladapter.NewFSPatchHandler(commandRunner),
		tooladapter.NewFSSearchHandler(commandRunner),
		tooladapter.NewConnListProfilesHandler(),
		tooladapter.NewConnDescribeProfileHandler(),
		tooladapter.NewNATSRequestHandler(nil),
		tooladapter.NewNATSSubscribePullHandler(nil),
		tooladapter.NewKafkaConsumeHandler(nil),
		tooladapter.NewKafkaTopicMetadataHandler(nil),
		tooladapter.NewRabbitConsumeHandler(nil),
		tooladapter.NewRabbitQueueInfoHandler(nil),
		tooladapter.NewRedisGetHandler(nil),
		tooladapter.NewRedisMGetHandler(nil),
		tooladapter.NewRedisScanHandler(nil),
		tooladapter.NewRedisTTLHandler(nil),
		tooladapter.NewRedisExistsHandler(nil),
		tooladapter.NewRedisSetHandler(nil),
		tooladapter.NewRedisDelHandler(nil),
		tooladapter.NewMongoFindHandler(nil),
		tooladapter.NewMongoAggregateHandler(nil),
		tooladapter.NewGitStatusHandler(commandRunner),
		tooladapter.NewGitDiffHandler(commandRunner),
		tooladapter.NewGitApplyPatchHandler(commandRunner),
		tooladapter.NewRepoDetectProjectTypeHandler(commandRunner),
		tooladapter.NewRepoDetectToolchainHandler(commandRunner),
		tooladapter.NewRepoValidateHandler(commandRunner),
		tooladapter.NewRepoBuildHandler(commandRunner),
		tooladapter.NewRepoTestHandler(commandRunner),
		tooladapter.NewRepoRunTestsHandler(commandRunner),
		tooladapter.NewRepoTestFailuresSummaryHandler(commandRunner),
		tooladapter.NewRepoStacktraceSummaryHandler(commandRunner),
		tooladapter.NewRepoChangedFilesHandler(commandRunner),
		tooladapter.NewRepoSymbolSearchHandler(commandRunner),
		tooladapter.NewRepoFindReferencesHandler(commandRunner),
		tooladapter.NewRepoCoverageReportHandler(commandRunner),
		tooladapter.NewRepoStaticAnalysisHandler(commandRunner),
		tooladapter.NewRepoPackageHandler(commandRunner),
		tooladapter.NewArtifactUploadHandler(commandRunner),
		tooladapter.NewArtifactDownloadHandler(commandRunner),
		tooladapter.NewArtifactListHandler(commandRunner),
		tooladapter.NewImageBuildHandler(commandRunner),
		tooladapter.NewImagePushHandler(commandRunner),
		tooladapter.NewImageInspectHandler(commandRunner),
		tooladapter.NewK8sGetPodsHandler(nil, "default"),
		tooladapter.NewK8sGetServicesHandler(nil, "default"),
		tooladapter.NewK8sGetDeploymentsHandler(nil, "default"),
		tooladapter.NewK8sGetImagesHandler(nil, "default"),
		tooladapter.NewK8sGetLogsHandler(nil, "default"),
		tooladapter.NewSecurityScanDependenciesHandler(commandRunner),
		tooladapter.NewSBOMGenerateHandler(commandRunner),
		tooladapter.NewSecurityScanSecretsHandler(commandRunner),
		tooladapter.NewSecurityScanContainerHandler(commandRunner),
		tooladapter.NewSecurityLicenseCheckHandler(commandRunner),
		tooladapter.NewQualityGateHandler(commandRunner),
		tooladapter.NewCIRunPipelineHandler(commandRunner),
		tooladapter.NewGoModTidyHandler(commandRunner),
		tooladapter.NewGoGenerateHandler(commandRunner),
		tooladapter.NewGoBuildHandler(commandRunner),
		tooladapter.NewGoTestHandler(commandRunner),
		tooladapter.NewRustBuildHandler(commandRunner),
		tooladapter.NewRustTestHandler(commandRunner),
		tooladapter.NewRustClippyHandler(commandRunner),
		tooladapter.NewRustFormatHandler(commandRunner),
		tooladapter.NewNodeInstallHandler(commandRunner),
		tooladapter.NewNodeBuildHandler(commandRunner),
		tooladapter.NewNodeTestHandler(commandRunner),
		tooladapter.NewNodeLintHandler(commandRunner),
		tooladapter.NewNodeTypecheckHandler(commandRunner),
		tooladapter.NewPythonInstallDepsHandler(commandRunner),
		tooladapter.NewPythonValidateHandler(commandRunner),
		tooladapter.NewPythonTestHandler(commandRunner),
		tooladapter.NewCBuildHandler(commandRunner),
		tooladapter.NewCTestHandler(commandRunner),
	)
	artifactStore := storage.NewLocalArtifactStore(artifactRoot)
	policyEngine := policy.NewStaticPolicy()
	auditLogger := audit.NewLoggerAudit(logger)
	service := app.NewService(workspaceManager, catalog, policyEngine, engine, artifactStore, auditLogger)

	if len(authCfg) > 0 {
		return NewServer(logger, service, authCfg[0]).Handler(), sourcePath
	}
	return NewServer(logger, service).Handler(), sourcePath
}

type testResponse struct {
	StatusCode int
	Body       *bytes.Buffer
}

func doJSONRequest(t *testing.T, handler http.Handler, method, path string, payload any) testResponse {
	t.Helper()
	return doJSONRequestWithHeaders(t, handler, method, path, payload, nil)
}

func doJSONRequestWithHeaders(
	t *testing.T,
	handler http.Handler,
	method, path string,
	payload any,
	headers map[string]string,
) testResponse {
	t.Helper()

	var bodyBytes []byte
	if payload != nil {
		switch typed := payload.(type) {
		case []byte:
			bodyBytes = typed
		case json.RawMessage:
			bodyBytes = []byte(typed)
		default:
			var err error
			bodyBytes, err = json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal payload failed: %v", err)
			}
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	buffer := &bytes.Buffer{}
	buffer.Write(resp.Body.Bytes())

	return testResponse{StatusCode: resp.Code, Body: buffer}
}

func mustDecode(t *testing.T, data []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, string(data))
	}
}

func TestHTTPAPI_MethodNotAllowedEdgeCases(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// POST /metrics → 405
	metricsResp := doJSONRequest(t, handler, http.MethodPost, "/metrics", map[string]any{})
	if metricsResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /metrics, got %d", metricsResp.StatusCode)
	}

	// Create a session to get a valid sessionID for route tests.
	createResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions", map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-a",
			"actor_id":  "agent-edge",
			"roles":     []string{"developer"},
		},
		"source_repo_path": sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create session, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody["session"].(map[string]any)["id"].(string)

	// GET /v1/sessions/{id} (len==1, non-DELETE) → 405
	sessionGetResp := doJSONRequest(t, handler, http.MethodGet, "/v1/sessions/"+sessionID, nil)
	if sessionGetResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 GET /v1/sessions/{id}, got %d", sessionGetResp.StatusCode)
	}

	// POST /v1/sessions/{id}/tools (len==2, non-GET) → 405
	toolsPostResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools", map[string]any{})
	if toolsPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/sessions/{id}/tools, got %d", toolsPostResp.StatusCode)
	}

	// DELETE /v1/sessions/{id}/tools/fs.read_file/invoke (len==4, non-POST) → 405
	invokeDeleteResp := doJSONRequest(t, handler, http.MethodDelete, "/v1/sessions/"+sessionID+"/tools/fs.read_file/invoke", nil)
	if invokeDeleteResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 DELETE /v1/sessions/{id}/tools/{tool}/invoke, got %d", invokeDeleteResp.StatusCode)
	}
}

func TestHTTPAPI_InvocationRouteEdgeCases(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// Create a session and invoke a tool to get a valid invocation ID.
	createResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions", map[string]any{
		"principal": map[string]any{
			"tenant_id": "tenant-a",
			"actor_id":  "agent-inv-edge",
			"roles":     []string{"developer"},
		},
		"source_repo_path": sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody["session"].(map[string]any)["id"].(string)

	invokeResp := doJSONRequest(t, handler, http.MethodPost, "/v1/sessions/"+sessionID+"/tools/fs.read_file/invoke", map[string]any{
		"args": map[string]any{"path": "seed.txt"},
	})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}
	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocationID := invokeBody["invocation"].(map[string]any)["id"].(string)

	// POST /v1/invocations/{id} (len==1, non-GET) → 405
	invPostResp := doJSONRequest(t, handler, http.MethodPost, "/v1/invocations/"+invocationID, map[string]any{})
	if invPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/invocations/{id}, got %d", invPostResp.StatusCode)
	}

	// POST /v1/invocations/{id}/artifacts (len==2 artifacts, non-GET) → 405
	artifactsPostResp := doJSONRequest(t, handler, http.MethodPost, "/v1/invocations/"+invocationID+"/artifacts", map[string]any{})
	if artifactsPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/invocations/{id}/artifacts, got %d", artifactsPostResp.StatusCode)
	}

	// GET /v1/invocations/{id}/unknown → 404 (route not found in invocations)
	invUnknownResp := doJSONRequest(t, handler, http.MethodGet, "/v1/invocations/"+invocationID+"/unknown", nil)
	if invUnknownResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 unknown invocation sub-route, got %d", invUnknownResp.StatusCode)
	}
}

func TestHTTPAPI_DecodeBodyNilBody(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	// Construct a POST /v1/sessions request with a nil body to exercise the decodeBody nil guard.
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Body = nil
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for nil body, got %d", resp.Code)
	}
}
