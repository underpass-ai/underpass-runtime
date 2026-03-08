package app_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/adapters/audit"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/policy"
	"github.com/underpass-ai/underpass-runtime/internal/adapters/storage"
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testTenantID                      = "tenant-a"
	testActorID                       = "alice"
	testRoleDeveloper                 = "developer"
	testUnexpectedCreateSessionErrFmt = "unexpected error creating session: %v"
	testNotesTodoPath                 = "notes/todo.txt"
)

func TestService_CreateAndListTools(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf(testUnexpectedCreateSessionErrFmt, err)
	}

	tools, listErr := svc.ListTools(ctx, session.ID)
	if listErr != nil {
		t.Fatalf("unexpected list error: %v", listErr)
	}
	if len(tools) == 0 {
		t.Fatal("expected tools to be listed")
	}

	found := false
	for _, tool := range tools {
		if tool.Name == "fs.list" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected fs.list to be available")
	}
}

func TestService_FsWriteRequiresApproval(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf(testUnexpectedCreateSessionErrFmt, err)
	}

	invocation, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": testNotesTodoPath, "content": "hello"}),
	})
	if invokeErr == nil {
		t.Fatal("expected approval error")
	}
	if invokeErr.Code != app.ErrorCodeApprovalRequired {
		t.Fatalf("unexpected error code: %s", invokeErr.Code)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation, got %s", invocation.Status)
	}
}

func TestService_FsWriteAndRead(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf(testUnexpectedCreateSessionErrFmt, err)
	}

	_, writeErr := svc.InvokeTool(ctx, session.ID, "fs.write_file", app.InvokeToolRequest{
		Approved: true,
		Args:     mustJSON(t, map[string]any{"path": testNotesTodoPath, "content": "hello world", "create_parents": true}),
	})
	if writeErr != nil {
		t.Fatalf("unexpected fs.write_file error: %v", writeErr)
	}

	invocation, readErr := svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": testNotesTodoPath}),
	})
	if readErr != nil {
		t.Fatalf("unexpected fs.read_file error: %v", readErr)
	}

	output, ok := invocation.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected output map, got %T", invocation.Output)
	}
	content, ok := output["content"].(string)
	if !ok {
		t.Fatalf("expected content string in output, got %#v", output["content"])
	}
	if content != "hello world" {
		t.Fatalf("unexpected file content: %q", content)
	}
}

func TestService_PathTraversalDenied(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf(testUnexpectedCreateSessionErrFmt, err)
	}

	_, invokeErr := svc.InvokeTool(ctx, session.ID, "fs.read_file", app.InvokeToolRequest{
		Args: mustJSON(t, map[string]any{"path": "../etc/passwd"}),
	})
	if invokeErr == nil {
		t.Fatal("expected traversal to be denied")
	}
	if invokeErr.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", invokeErr.Code)
	}
}

func setupService(t *testing.T) *app.Service {
	t.Helper()

	workspaceRoot := t.TempDir()
	artifactRoot := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	workspaceManager := workspaceadapter.NewLocalManager(workspaceRoot)
	catalog := tooladapter.NewCatalog(tooladapter.DefaultCapabilities())
	commandRunner := tooladapter.NewLocalCommandRunner()
	handlers := []tooladapter.Handler{ //nolint:prealloc // k8s handlers appended conditionally via build tags
		tooladapter.NewFSListHandler(commandRunner),
		tooladapter.NewFSReadHandler(commandRunner),
		tooladapter.NewFSWriteHandler(commandRunner),
		tooladapter.NewFSMkdirHandler(commandRunner),
		tooladapter.NewFSMoveHandler(commandRunner),
		tooladapter.NewFSCopyHandler(commandRunner),
		tooladapter.NewFSDeleteHandler(commandRunner),
		tooladapter.NewFSStatHandler(commandRunner),
		tooladapter.NewFSPatchHandler(commandRunner),
		tooladapter.NewFSSearchHandler(commandRunner),
		tooladapter.NewConnListProfilesHandler(),
		tooladapter.NewConnDescribeProfileHandler(),
		tooladapter.NewAPIBenchmarkHandler(commandRunner),
		tooladapter.NewNATSRequestHandler(nil),
		tooladapter.NewNATSPublishHandler(nil),
		tooladapter.NewNATSSubscribePullHandler(nil),
		tooladapter.NewKafkaConsumeHandler(nil),
		tooladapter.NewKafkaProduceHandler(nil),
		tooladapter.NewKafkaTopicMetadataHandler(nil),
		tooladapter.NewRabbitConsumeHandler(nil),
		tooladapter.NewRabbitPublishHandler(nil),
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
		tooladapter.NewGitCheckoutHandler(commandRunner),
		tooladapter.NewGitLogHandler(commandRunner),
		tooladapter.NewGitShowHandler(commandRunner),
		tooladapter.NewGitBranchListHandler(commandRunner),
		tooladapter.NewGitCommitHandler(commandRunner),
		tooladapter.NewGitPushHandler(commandRunner),
		tooladapter.NewGitFetchHandler(commandRunner),
		tooladapter.NewGitPullHandler(commandRunner),
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
		tooladapter.NewContainerPSHandler(commandRunner),
		tooladapter.NewContainerLogsHandler(commandRunner),
		tooladapter.NewContainerRunHandler(commandRunner),
		tooladapter.NewContainerExecHandler(commandRunner),
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
	return app.NewService(workspaceManager, catalog, policyEngine, engine, artifactStore, auditLogger)
}

func TestService_DiscoverTools(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID, ActorID: testActorID, Roles: []string{testRoleDeveloper}},
	})
	if err != nil {
		t.Fatalf(testUnexpectedCreateSessionErrFmt, err)
	}

	discovery, discErr := svc.DiscoverTools(ctx, session.ID)
	if discErr != nil {
		t.Fatalf("unexpected discovery error: %v", discErr)
	}
	if len(discovery.Tools) == 0 {
		t.Fatal("expected tools in discovery response")
	}
	if discovery.Total == 0 {
		t.Fatal("expected total > 0")
	}
	if discovery.Filtered > discovery.Total {
		t.Fatalf("filtered (%d) should not exceed total (%d)", discovery.Filtered, discovery.Total)
	}

	// Verify compact fields
	first := discovery.Tools[0]
	if first.Name == "" {
		t.Fatal("expected non-empty tool name")
	}
	if first.Description == "" {
		t.Fatal("expected non-empty description")
	}
	if first.Risk == "" {
		t.Fatal("expected non-empty risk")
	}
	if len(first.Tags) == 0 {
		t.Fatal("expected at least one tag")
	}
	if first.Cost == "" {
		t.Fatal("expected non-empty cost")
	}
	if len(first.Description) > 120 {
		t.Fatalf("description should be <=120 chars, got %d", len(first.Description))
	}

	// Verify tools are sorted
	for i := 1; i < len(discovery.Tools); i++ {
		if discovery.Tools[i].Name < discovery.Tools[i-1].Name {
			t.Fatalf("tools not sorted: %s before %s", discovery.Tools[i-1].Name, discovery.Tools[i].Name)
		}
	}
}

func TestService_DiscoverTools_InvalidSession(t *testing.T) {
	svc := setupService(t)
	_, discErr := svc.DiscoverTools(context.Background(), "nonexistent")
	if discErr == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func mustJSON(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}
