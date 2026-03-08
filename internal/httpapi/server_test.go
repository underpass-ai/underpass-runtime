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

const (
	testHTTPTenantAuth        = "tenant-auth"
	testHTTPRoleDeveloper     = "developer"
	testHTTPSharedToken       = "workspace-shared-token"
	testHTTPActorAuth         = "actor-auth"
	testHTTPKeyTenantID       = "tenant_id"
	testHTTPKeyActorID        = "actor_id"
	testHTTPKeyRoles          = "roles"
	testHTTPKeyPrincipal      = "principal"
	testHTTPKeySession        = "session"
	testHTTPKeyID             = "id"
	testHTTPKeyInvocation     = "invocation"
	testHTTPSourceRepoPath    = "source_repo_path"
	testPathMetrics           = "/metrics"
	testPathSessionTools      = "/tools"
	testPathFSReadFileInvoke  = "/tools/fs.read_file/invoke"
	testPathInvocationsPrefix = "/v1/invocations/"
	testPathSessionsPrefix    = "/v1/sessions/"
	testSeedFile              = "seed.txt"
)

func TestHTTPAPI_EndToEndToolExecutionInWorkspace(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createPayload := map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-1",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
		"expires_in_seconds":   3600,
	}
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, createPayload)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	session := createBody[testHTTPKeySession].(map[string]any)
	sessionID := session[testHTTPKeyID].(string)
	workspacePath := session["workspace_path"].(string)

	invokePayload := map[string]any{
		"approved": true,
		"args": map[string]any{
			"path":           "notes/result.txt",
			"content":        "workspace ok",
			"create_parents": true,
		},
	}
	invokeResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+"/tools/fs.write_file/invoke", invokePayload)
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}

	if _, err := os.Stat(filepath.Join(workspacePath, "notes", "result.txt")); err != nil {
		t.Fatalf("expected written file in workspace, got error: %v", err)
	}

	readPayload := map[string]any{"args": map[string]any{"path": "notes/result.txt"}}
	readResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, readPayload)
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on fs.read_file, got %d body=%s", readResp.StatusCode, readResp.Body.String())
	}

	var readBody map[string]any
	mustDecode(t, readResp.Body.Bytes(), &readBody)
	invocation := readBody[testHTTPKeyInvocation].(map[string]any)
	output := invocation["output"].(map[string]any)
	if output["content"].(string) != "workspace ok" {
		t.Fatalf("unexpected read content: %#v", output)
	}
}

func TestHTTPAPI_ApprovalRequiredAndRouteErrors(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	createPayload := map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-2",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
	}
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, createPayload)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	denyPayload := map[string]any{
		"approved": false,
		"args": map[string]any{
			"path":    "x.txt",
			"content": "denied",
		},
	}
	denyResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+"/tools/fs.write_file/invoke", denyPayload)
	if denyResp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected 428, got %d body=%s", denyResp.StatusCode, denyResp.Body.String())
	}

	notFoundResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/unknown", nil)
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", notFoundResp.StatusCode)
	}

	methodResp := doJSONRequest(t, handler, http.MethodGet, testSharedSessionsPath, nil)
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
	metricsResp := doJSONRequest(t, handler, http.MethodGet, testPathMetrics, nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metrics, got %d body=%s", metricsResp.StatusCode, metricsResp.Body.String())
	}
	if !strings.Contains(metricsResp.Body.String(), "invocations_total") {
		t.Fatalf("expected metrics payload, got: %s", metricsResp.Body.String())
	}

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-3",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	invokeResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
		"args": map[string]any{"path": testSeedFile},
	})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}

	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocationID := invokeBody[testHTTPKeyInvocation].(map[string]any)[testHTTPKeyID].(string)

	getResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation get, got %d", getResp.StatusCode)
	}

	logsResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID+"/logs", nil)
	if logsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation logs, got %d", logsResp.StatusCode)
	}

	artifactsResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID+"/artifacts", nil)
	if artifactsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invocation artifacts, got %d", artifactsResp.StatusCode)
	}

	postInvokeMetrics := doJSONRequest(t, handler, http.MethodGet, testPathMetrics, nil)
	if postInvokeMetrics.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metrics after invoke, got %d", postInvokeMetrics.StatusCode)
	}
	if !strings.Contains(postInvokeMetrics.Body.String(), `invocations_total{tool="fs.read_file",status="succeeded"} 1`) {
		t.Fatalf("expected fs.read_file succeeded counter in metrics, got: %s", postInvokeMetrics.Body.String())
	}

	malformedResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, json.RawMessage(`{"args":`))
	if malformedResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 malformed invoke body, got %d", malformedResp.StatusCode)
	}

	invMethodResp := doJSONRequest(t, handler, http.MethodPost, testPathInvocationsPrefix+invocationID+"/logs", map[string]any{})
	if invMethodResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 invocation logs method, got %d", invMethodResp.StatusCode)
	}

	missingInvResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+"missing", nil)
	if missingInvResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 missing invocation, got %d", missingInvResp.StatusCode)
	}
}

func TestHTTPAPI_TrustedHeadersUsesAuthenticatedPrincipal(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = testHTTPSharedToken

	handler, _ := setupHTTPHandler(t, authCfg)
	headers := map[string]string{
		authCfg.TokenHeader:  testHTTPSharedToken,
		authCfg.TenantHeader: testHTTPTenantAuth,
		authCfg.ActorHeader:  testHTTPActorAuth,
		authCfg.RolesHeader:  "devops,developer",
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: "payload-tenant",
			testHTTPKeyActorID:  "payload-actor",
			testHTTPKeyRoles:    []string{"admin"},
		},
	}, headers)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create in trusted mode, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}

	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	session := createBody[testHTTPKeySession].(map[string]any)
	principal := session[testHTTPKeyPrincipal].(map[string]any)
	if principal[testHTTPKeyTenantID] != testHTTPTenantAuth || principal[testHTTPKeyActorID] != testHTTPActorAuth {
		t.Fatalf("expected principal from trusted headers, got %#v", principal)
	}
}

func TestHTTPAPI_TrustedHeadersRejectsMissingToken(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = testHTTPSharedToken

	handler, _ := setupHTTPHandler(t, authCfg)
	headers := map[string]string{
		authCfg.TenantHeader: testHTTPTenantAuth,
		authCfg.ActorHeader:  testHTTPActorAuth,
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testHTTPTenantAuth,
			testHTTPKeyActorID:  testHTTPActorAuth,
		},
	}, headers)
	if createResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized without token, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
}

func TestHTTPAPI_TrustedHeadersEnforcesSessionOwnership(t *testing.T) {
	authCfg := DefaultAuthConfig()
	authCfg.Mode = authModeTrustedHeaders
	authCfg.SharedToken = testHTTPSharedToken

	handler, _ := setupHTTPHandler(t, authCfg)
	ownerHeaders := map[string]string{
		authCfg.TokenHeader:  testHTTPSharedToken,
		authCfg.TenantHeader: testHTTPTenantAuth,
		authCfg.ActorHeader:  "actor-owner",
		authCfg.RolesHeader:  testHTTPRoleDeveloper,
	}

	createResp := doJSONRequestWithHeaders(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{}, ownerHeaders)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	ownerListResp := doJSONRequestWithHeaders(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+testPathSessionTools, nil, ownerHeaders)
	if ownerListResp.StatusCode != http.StatusOK {
		t.Fatalf("expected owner access to session tools, got %d body=%s", ownerListResp.StatusCode, ownerListResp.Body.String())
	}

	otherHeaders := map[string]string{
		authCfg.TokenHeader:  testHTTPSharedToken,
		authCfg.TenantHeader: testHTTPTenantAuth,
		authCfg.ActorHeader:  "actor-other",
		authCfg.RolesHeader:  testHTTPRoleDeveloper,
	}
	otherListResp := doJSONRequestWithHeaders(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+testPathSessionTools, nil, otherHeaders)
	if otherListResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d body=%s", otherListResp.StatusCode, otherListResp.Body.String())
	}
}

func setupHTTPHandler(t *testing.T, authCfg ...AuthConfig) (http.Handler, string) {
	t.Helper()

	workspaceRoot := t.TempDir()
	artifactRoot := t.TempDir()
	sourcePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourcePath, testSeedFile), []byte("seed"), 0o644); err != nil {
		t.Fatalf("write source seed failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	workspaceManager := workspaceadapter.NewLocalManager(workspaceRoot)
	catalog := tooladapter.NewCatalog(tooladapter.DefaultCapabilities())
	commandRunner := tooladapter.NewLocalCommandRunner()
	handlers := []tooladapter.Handler{ //nolint:prealloc // k8s handlers appended conditionally via build tags
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
	}
	handlers = append(handlers, k8sToolHandlers()...)
	engine := tooladapter.NewEngine(handlers...)
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
	metricsResp := doJSONRequest(t, handler, http.MethodPost, testPathMetrics, map[string]any{})
	if metricsResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /metrics, got %d", metricsResp.StatusCode)
	}

	// Create a session to get a valid sessionID for route tests.
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-edge",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create session, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// GET /v1/sessions/{id} (len==1, non-DELETE) → 405
	sessionGetResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID, nil)
	if sessionGetResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 GET /v1/sessions/{id}, got %d", sessionGetResp.StatusCode)
	}

	// POST /v1/sessions/{id}/tools (len==2, non-GET) → 405
	toolsPostResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathSessionTools, map[string]any{})
	if toolsPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/sessions/{id}/tools, got %d", toolsPostResp.StatusCode)
	}

	// DELETE /v1/sessions/{id}/tools/fs.read_file/invoke (len==4, non-POST) → 405
	invokeDeleteResp := doJSONRequest(t, handler, http.MethodDelete, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, nil)
	if invokeDeleteResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 DELETE /v1/sessions/{id}/tools/{tool}/invoke, got %d", invokeDeleteResp.StatusCode)
	}
}

func TestHTTPAPI_InvocationRouteEdgeCases(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	// Create a session and invoke a tool to get a valid invocation ID.
	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-inv-edge",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	invokeResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+testPathFSReadFileInvoke, map[string]any{
		"args": map[string]any{"path": testSeedFile},
	})
	if invokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 invoke, got %d body=%s", invokeResp.StatusCode, invokeResp.Body.String())
	}
	var invokeBody map[string]any
	mustDecode(t, invokeResp.Body.Bytes(), &invokeBody)
	invocationID := invokeBody[testHTTPKeyInvocation].(map[string]any)[testHTTPKeyID].(string)

	// POST /v1/invocations/{id} (len==1, non-GET) → 405
	invPostResp := doJSONRequest(t, handler, http.MethodPost, testPathInvocationsPrefix+invocationID, map[string]any{})
	if invPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/invocations/{id}, got %d", invPostResp.StatusCode)
	}

	// POST /v1/invocations/{id}/artifacts (len==2 artifacts, non-GET) → 405
	artifactsPostResp := doJSONRequest(t, handler, http.MethodPost, testPathInvocationsPrefix+invocationID+"/artifacts", map[string]any{})
	if artifactsPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 POST /v1/invocations/{id}/artifacts, got %d", artifactsPostResp.StatusCode)
	}

	// GET /v1/invocations/{id}/unknown → 404 (route not found in invocations)
	invUnknownResp := doJSONRequest(t, handler, http.MethodGet, testPathInvocationsPrefix+invocationID+"/unknown", nil)
	if invUnknownResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 unknown invocation sub-route, got %d", invUnknownResp.StatusCode)
	}
}

func TestHTTPAPI_DiscoveryEndpoint(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-discovery",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	discoveryResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/tools/discovery", nil)
	if discoveryResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", discoveryResp.StatusCode, discoveryResp.Body.String())
	}

	var body map[string]any
	mustDecode(t, discoveryResp.Body.Bytes(), &body)

	tools, ok := body["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", body["tools"])
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool in discovery response")
	}

	total := body["total"].(float64)
	filtered := body["filtered"].(float64)
	if total < filtered {
		t.Fatalf("total (%v) should be >= filtered (%v)", total, filtered)
	}

	// Check first tool has required compact fields
	first := tools[0].(map[string]any)
	for _, field := range []string{"name", "description", "required_args", "risk", "side_effects", "tags", "cost"} {
		if _, exists := first[field]; !exists {
			t.Fatalf("compact tool missing field %q", field)
		}
	}

	// Verify response is significantly smaller than full ListTools
	fullResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+testPathSessionTools, nil)
	if len(discoveryResp.Body.Bytes()) >= len(fullResp.Body.Bytes()) {
		t.Fatalf("discovery response (%d bytes) should be smaller than full tools response (%d bytes)",
			len(discoveryResp.Body.Bytes()), len(fullResp.Body.Bytes()))
	}
}

func TestHTTPAPI_DiscoveryEndpoint_MethodNotAllowed(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-disc-405",
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

	postResp := doJSONRequest(t, handler, http.MethodPost, testPathSessionsPrefix+sessionID+"/tools/discovery", map[string]any{})
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", postResp.StatusCode)
	}
}

func TestHTTPAPI_DiscoveryEndpoint_InvalidSession(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	resp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+"nonexistent-session/tools/discovery", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, resp.Body.String())
	}
}

func TestHTTPAPI_DiscoveryEndpoint_FilterByRisk(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-disc-filter",
			testHTTPKeyRoles:    []string{testHTTPRoleDeveloper},
		},
		testHTTPSourceRepoPath: sourcePath,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createResp.StatusCode, createResp.Body.String())
	}
	var createBody map[string]any
	mustDecode(t, createResp.Body.Bytes(), &createBody)
	sessionID := createBody[testHTTPKeySession].(map[string]any)[testHTTPKeyID].(string)

	// Unfiltered
	allResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/tools/discovery", nil)
	var allBody map[string]any
	mustDecode(t, allResp.Body.Bytes(), &allBody)
	allTools := allBody["tools"].([]any)

	// Filter by risk=low
	filteredResp := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/tools/discovery?risk=low", nil)
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", filteredResp.StatusCode)
	}
	var filteredBody map[string]any
	mustDecode(t, filteredResp.Body.Bytes(), &filteredBody)
	filteredTools := filteredBody["tools"].([]any)

	if len(filteredTools) == 0 {
		t.Fatal("expected at least one low-risk tool")
	}
	if len(filteredTools) >= len(allTools) {
		t.Fatal("risk=low filter should return fewer tools")
	}

	for _, raw := range filteredTools {
		tool := raw.(map[string]any)
		if tool["risk"] != "low" {
			t.Fatalf("expected risk=low, got %v for tool %v", tool["risk"], tool["name"])
		}
	}

	filteredCount := filteredBody["filtered"].(float64)
	if int(filteredCount) != len(filteredTools) {
		t.Fatalf("filtered count (%v) != tools length (%d)", filteredCount, len(filteredTools))
	}
}

func TestHTTPAPI_DiscoveryEndpoint_FilterByMultipleParams(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-disc-multi",
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

	// Combined filter: risk=low AND side_effects=none
	resp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/discovery?risk=low&side_effects=none", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	mustDecode(t, resp.Body.Bytes(), &body)
	tools := body["tools"].([]any)

	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["risk"] != "low" {
			t.Fatalf("expected risk=low, got %v", tool["risk"])
		}
		if tool["side_effects"] != "none" {
			t.Fatalf("expected side_effects=none, got %v", tool["side_effects"])
		}
	}
}

func TestHTTPAPI_DiscoveryEndpoint_FilterCSVValues(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-disc-csv",
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

	// CSV risk=low,medium (OR within field)
	resp := doJSONRequest(t, handler, http.MethodGet,
		testPathSessionsPrefix+sessionID+"/tools/discovery?risk=low,medium", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	mustDecode(t, resp.Body.Bytes(), &body)
	tools := body["tools"].([]any)

	for _, raw := range tools {
		tool := raw.(map[string]any)
		risk := tool["risk"].(string)
		if risk != "low" && risk != "medium" {
			t.Fatalf("expected risk=low or medium, got %s", risk)
		}
	}
}

func TestHTTPAPI_DiscoveryEndpoint_EmptyFilterReturnsAll(t *testing.T) {
	handler, sourcePath := setupHTTPHandler(t)

	createResp := doJSONRequest(t, handler, http.MethodPost, testSharedSessionsPath, map[string]any{
		testHTTPKeyPrincipal: map[string]any{
			testHTTPKeyTenantID: testSharedTenantA,
			testHTTPKeyActorID:  "agent-disc-empty",
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

	noFilter := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/tools/discovery", nil)
	withEmpty := doJSONRequest(t, handler, http.MethodGet, testPathSessionsPrefix+sessionID+"/tools/discovery?risk=&tags=", nil)

	var noFilterBody, emptyBody map[string]any
	mustDecode(t, noFilter.Body.Bytes(), &noFilterBody)
	mustDecode(t, withEmpty.Body.Bytes(), &emptyBody)

	noFilterCount := noFilterBody["filtered"].(float64)
	emptyCount := emptyBody["filtered"].(float64)
	if noFilterCount != emptyCount {
		t.Fatalf("empty string params should behave like no filter: %v vs %v", noFilterCount, emptyCount)
	}
}

func TestHTTPAPI_DecodeBodyNilBody(t *testing.T) {
	handler, _ := setupHTTPHandler(t)

	// Construct a POST /v1/sessions request with a nil body to exercise the decodeBody nil guard.
	req := httptest.NewRequest(http.MethodPost, testSharedSessionsPath, nil)
	req.Body = nil
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for nil body, got %d", resp.Code)
	}
}
