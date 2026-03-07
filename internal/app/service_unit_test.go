package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeWorkspaceManager struct {
	session   domain.Session
	found     bool
	createErr error
	getErr    error
	closeErr  error
}

func (f *fakeWorkspaceManager) CreateSession(_ context.Context, _ CreateSessionRequest) (domain.Session, error) {
	if f.createErr != nil {
		return domain.Session{}, f.createErr
	}
	return f.session, nil
}

func (f *fakeWorkspaceManager) GetSession(_ context.Context, _ string) (domain.Session, bool, error) {
	if f.getErr != nil {
		return domain.Session{}, false, f.getErr
	}
	return f.session, f.found, nil
}

func (f *fakeWorkspaceManager) CloseSession(_ context.Context, _ string) error {
	return f.closeErr
}

type fakeCatalog struct {
	entries map[string]domain.Capability
}

func (f *fakeCatalog) Get(name string) (domain.Capability, bool) {
	entry, ok := f.entries[name]
	return entry, ok
}

func (f *fakeCatalog) List() []domain.Capability {
	out := make([]domain.Capability, 0, len(f.entries))
	for _, entry := range f.entries {
		out = append(out, entry)
	}
	return out
}

type fakePolicyEngine struct {
	decision PolicyDecision
	err      error
}

func (f *fakePolicyEngine) Authorize(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
	if f.err != nil {
		return PolicyDecision{}, f.err
	}
	return f.decision, nil
}

type fakeToolEngine struct {
	result ToolRunResult
	err    *domain.Error
	calls  int
}

func (f *fakeToolEngine) Invoke(_ context.Context, _ domain.Session, _ domain.Capability, _ json.RawMessage) (ToolRunResult, *domain.Error) {
	f.calls++
	return f.result, f.err
}

type blockingToolEngine struct {
	started chan struct{}
	release chan struct{}
	result  ToolRunResult
	err     *domain.Error
}

func (b *blockingToolEngine) Invoke(_ context.Context, _ domain.Session, _ domain.Capability, _ json.RawMessage) (ToolRunResult, *domain.Error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	if b.release != nil {
		<-b.release
	}
	return b.result, b.err
}

type fakeArtifactStore struct {
	saved      []domain.Artifact
	saveErr    error
	listed     []domain.Artifact
	listErr    error
	readByPath map[string][]byte
	readErr    error
}

func (f *fakeArtifactStore) Save(_ context.Context, _ string, _ []ArtifactPayload) ([]domain.Artifact, error) {
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	return f.saved, nil
}

func (f *fakeArtifactStore) List(_ context.Context, _ string) ([]domain.Artifact, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listed, nil
}

func (f *fakeArtifactStore) Read(_ context.Context, path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.readByPath == nil {
		return nil, errors.New("artifact not found")
	}
	data, ok := f.readByPath[path]
	if !ok {
		return nil, errors.New("artifact not found")
	}
	return data, nil
}

type fakeAudit struct{}

func (f *fakeAudit) Record(_ context.Context, _ AuditEvent) {}

type fakeInvocationStore struct {
	data    map[string]domain.Invocation
	saveErr error
	getErr  error
}

func (f *fakeInvocationStore) Save(_ context.Context, invocation domain.Invocation) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.data == nil {
		f.data = map[string]domain.Invocation{}
	}
	f.data[invocation.ID] = invocation
	return nil
}

func (f *fakeInvocationStore) Get(_ context.Context, invocationID string) (domain.Invocation, bool, error) {
	if f.getErr != nil {
		return domain.Invocation{}, false, f.getErr
	}
	invocation, ok := f.data[invocationID]
	return invocation, ok, nil
}

func defaultSession() domain.Session {
	return domain.Session{
		ID:            "session-1",
		WorkspacePath: "/tmp/workspace",
		AllowedPaths:  []string{"."},
		Principal: domain.Principal{
			TenantID: "tenant-a",
			ActorID:  "alice",
			Roles:    []string{"developer"},
		},
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

func defaultCapability() domain.Capability {
	return domain.Capability{
		Name:          "fs.read",
		Observability: domain.Observability{TraceName: "trace", SpanName: "span"},
		Constraints:   domain.Constraints{TimeoutSeconds: 1},
	}
}

func newServiceForTest(
	workspace WorkspaceManager,
	catalog CapabilityRegistry,
	policy Authorizer,
	tools Invoker,
	artifacts ArtifactStore,
) *Service {
	return NewService(workspace, catalog, policy, tools, artifacts, &fakeAudit{})
}

func TestCreateSessionValidationAndErrors(t *testing.T) {
	svc := newServiceForTest(&fakeWorkspaceManager{}, &fakeCatalog{}, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})

	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{Principal: domain.Principal{ActorID: "a"}})
	if err == nil || err.Code != ErrorCodeInvalidArgument {
		t.Fatalf("expected tenant validation error, got %#v", err)
	}

	_, err = svc.CreateSession(context.Background(), CreateSessionRequest{Principal: domain.Principal{TenantID: "t"}})
	if err == nil || err.Code != ErrorCodeInvalidArgument {
		t.Fatalf("expected actor validation error, got %#v", err)
	}

	workspace := &fakeWorkspaceManager{createErr: errors.New("boom")}
	svc = newServiceForTest(workspace, &fakeCatalog{}, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})
	_, err = svc.CreateSession(context.Background(), CreateSessionRequest{Principal: domain.Principal{TenantID: "t", ActorID: "a"}})
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal create error, got %#v", err)
	}
}

func TestCloseSessionValidationAndErrors(t *testing.T) {
	svc := newServiceForTest(&fakeWorkspaceManager{}, &fakeCatalog{}, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})

	err := svc.CloseSession(context.Background(), "")
	if err == nil || err.Code != ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument, got %#v", err)
	}

	svc = newServiceForTest(&fakeWorkspaceManager{closeErr: errors.New("close")}, &fakeCatalog{}, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})
	err = svc.CloseSession(context.Background(), "session-1")
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal close error, got %#v", err)
	}
}

func TestValidateSessionAccess(t *testing.T) {
	session := defaultSession()
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{},
		&fakePolicyEngine{},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)

	err := svc.ValidateSessionAccess(context.Background(), session.ID, session.Principal)
	if err != nil {
		t.Fatalf("expected access allowed, got %#v", err)
	}

	err = svc.ValidateSessionAccess(context.Background(), session.ID, domain.Principal{
		TenantID: session.Principal.TenantID,
		ActorID:  "another-actor",
	})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected policy denied for principal mismatch, got %#v", err)
	}
}

func TestValidateInvocationAccess(t *testing.T) {
	session := defaultSession()
	invStore := &fakeInvocationStore{
		data: map[string]domain.Invocation{
			"inv-1": {
				ID:        "inv-1",
				SessionID: session.ID,
			},
		},
	}
	svc := NewService(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{},
		&fakePolicyEngine{},
		&fakeToolEngine{},
		&fakeArtifactStore{},
		&fakeAudit{},
		invStore,
	)

	if err := svc.ValidateInvocationAccess(context.Background(), "inv-1", session.Principal); err != nil {
		t.Fatalf("expected invocation access allowed, got %#v", err)
	}

	err := svc.ValidateInvocationAccess(context.Background(), "inv-1", domain.Principal{
		TenantID: session.Principal.TenantID,
		ActorID:  "other-actor",
	})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected policy denied, got %#v", err)
	}

	err = svc.ValidateInvocationAccess(context.Background(), "inv-missing", session.Principal)
	if err == nil || err.Code != ErrorCodeNotFound {
		t.Fatalf("expected not found invocation, got %#v", err)
	}
}

func TestListToolsFiltersAndErrors(t *testing.T) {
	session := defaultSession()
	catalog := &fakeCatalog{entries: map[string]domain.Capability{
		"a": {Name: "a"},
		"b": {Name: "b"},
	}}

	svc := newServiceForTest(&fakeWorkspaceManager{session: session, found: false}, catalog, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})
	_, err := svc.ListTools(context.Background(), "session-1")
	if err == nil || err.Code != ErrorCodeNotFound {
		t.Fatalf("expected not found, got %#v", err)
	}

	svc = newServiceForTest(&fakeWorkspaceManager{getErr: errors.New("get")}, catalog, &fakePolicyEngine{}, &fakeToolEngine{}, &fakeArtifactStore{})
	_, err = svc.ListTools(context.Background(), "session-1")
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal get error, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		catalog,
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	tools, err := svc.ListTools(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected two tools, got %d", len(tools))
	}
}

func TestListToolsHidesClusterScopeWhenRuntimeIsNotKubernetes(t *testing.T) {
	session := defaultSession()
	catalog := &fakeCatalog{entries: map[string]domain.Capability{
		"fs.list": {
			Name:  "fs.list",
			Scope: domain.ScopeWorkspace,
		},
		"k8s.get_pods": {
			Name:  "k8s.get_pods",
			Scope: domain.ScopeCluster,
		},
	}}

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		catalog,
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	tools, err := svc.ListTools(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fs.list" {
		t.Fatalf("expected only workspace tool for local runtime, got %#v", tools)
	}

	session.Runtime.Kind = domain.RuntimeKindKubernetes
	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		catalog,
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	tools, err = svc.ListTools(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("unexpected list error for kubernetes runtime: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected both tools for kubernetes runtime, got %#v", tools)
	}
}

func TestListToolsHidesK8sDeliveryToolsWhenDisabled(t *testing.T) {
	session := defaultSession()
	session.Runtime.Kind = domain.RuntimeKindKubernetes

	catalog := &fakeCatalog{entries: map[string]domain.Capability{
		"k8s.get_pods": {
			Name:  "k8s.get_pods",
			Scope: domain.ScopeCluster,
		},
		"k8s.apply_manifest": {
			Name:  "k8s.apply_manifest",
			Scope: domain.ScopeCluster,
		},
	}}

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		catalog,
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	tools, err := svc.ListTools(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "k8s.get_pods" {
		t.Fatalf("expected delivery tools hidden by default, got %#v", tools)
	}

	t.Setenv("WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS", "true")
	tools, err = svc.ListTools(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("unexpected list error with delivery enabled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected both cluster tools when delivery enabled, got %#v", tools)
	}
}

func TestInvokeToolValidationAndPolicyBranches(t *testing.T) {
	session := defaultSession()
	capability := defaultCapability()

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: false},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	_, err := svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeNotFound {
		t.Fatalf("expected session not found, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	_, err = svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeNotFound {
		t.Fatalf("expected tool not found, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{err: errors.New("policy down")},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	_, err = svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected policy internal error, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: false, ErrorCode: ErrorCodeApprovalRequired, Reason: "approval needed"}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	invocation, err := svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeApprovalRequired {
		t.Fatalf("expected approval required, got %#v", err)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied status, got %s", invocation.Status)
	}
}

func TestInvokeTool_DeniesWhenRateLimitExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_RATE_LIMIT_PER_MINUTE", "1")

	session := defaultSession()
	capability := defaultCapability()
	tool := &fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}}}
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		tool,
		&fakeArtifactStore{},
	)

	first, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("unexpected first invocation error: %#v", err)
	}
	if first.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("expected first invocation succeeded, got %#v", first.Status)
	}

	second, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected rate-limit policy denied, got %#v", err)
	}
	if second.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation status, got %#v", second.Status)
	}
	if second.Error == nil || !strings.Contains(second.Error.Message, "rate limit") {
		t.Fatalf("expected rate-limit message, got %#v", second.Error)
	}
}

func TestInvocationQuotaLimiter_DeniesWhenPrincipalRateLimitExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_RATE_LIMIT_PER_MINUTE_PER_PRINCIPAL", "1")
	limiter := newInvocationQuotaLimiterFromEnv()
	now := time.Date(2026, 2, 21, 19, 0, 1, 0, time.UTC)

	sessionA := defaultSession()
	sessionA.ID = "session-a"
	sessionB := defaultSession()
	sessionB.ID = "session-b"

	allowed, reason := limiter.allowRate(sessionA, now)
	if !allowed {
		t.Fatalf("expected first invocation to pass principal rate limit, got reason=%q", reason)
	}

	allowed, reason = limiter.allowRate(sessionB, now)
	if allowed {
		t.Fatal("expected second invocation for same principal to be denied")
	}
	if reason != "principal invocation rate limit exceeded" {
		t.Fatalf("expected principal rate reason, got %q", reason)
	}

	sessionC := defaultSession()
	sessionC.ID = "session-c"
	sessionC.Principal.ActorID = "bob"

	allowed, reason = limiter.allowRate(sessionC, now)
	if !allowed {
		t.Fatalf("expected different principal to pass rate limit, got reason=%q", reason)
	}
}

func TestInvokeTool_DeniesWhenConcurrencyLimitExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_MAX_CONCURRENCY_PER_SESSION", "1")

	session := defaultSession()
	capability := defaultCapability()
	blockingTool := &blockingToolEngine{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		result:  ToolRunResult{Output: map[string]any{"ok": true}},
	}
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		blockingTool,
		&fakeArtifactStore{},
	)

	type invocationResult struct {
		invocation domain.Invocation
		err        *ServiceError
	}
	firstDone := make(chan invocationResult, 1)
	go func() {
		invocation, invokeErr := svc.InvokeTool(
			context.Background(),
			session.ID,
			capability.Name,
			InvokeToolRequest{Args: json.RawMessage(`{}`)},
		)
		firstDone <- invocationResult{invocation: invocation, err: invokeErr}
	}()

	select {
	case <-blockingTool.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first invocation to start")
	}

	second, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected concurrency policy denied, got %#v", err)
	}
	if second.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation status, got %#v", second.Status)
	}
	if second.Error == nil || !strings.Contains(second.Error.Message, "concurrency limit") {
		t.Fatalf("expected concurrency limit message, got %#v", second.Error)
	}

	close(blockingTool.release)

	select {
	case first := <-firstDone:
		if first.err != nil {
			t.Fatalf("unexpected first invocation error: %#v", first.err)
		}
		if first.invocation.Status != domain.InvocationStatusSucceeded {
			t.Fatalf("expected first invocation succeeded, got %#v", first.invocation.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first invocation to complete")
	}
}

func TestInvokeTool_DeniesWhenOutputQuotaExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_MAX_OUTPUT_BYTES_PER_INVOCATION", "8")

	session := defaultSession()
	capability := defaultCapability()
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"payload": "0123456789"}}},
		&fakeArtifactStore{},
	)

	invocation, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected output quota policy denied, got %#v", err)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation status, got %#v", invocation.Status)
	}
	if invocation.Error == nil || !strings.Contains(invocation.Error.Message, "output size quota exceeded") {
		t.Fatalf("expected output quota message, got %#v", invocation.Error)
	}
}

func TestInvokeTool_DeniesWhenArtifactCountQuotaExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_MAX_ARTIFACTS_PER_INVOCATION", "1")

	session := defaultSession()
	capability := defaultCapability()
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Artifacts: []ArtifactPayload{
			{Name: "a.txt", ContentType: "text/plain", Data: []byte("a")},
			{Name: "b.txt", ContentType: "text/plain", Data: []byte("b")},
		}}},
		&fakeArtifactStore{},
	)

	invocation, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected artifact count quota policy denied, got %#v", err)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation status, got %#v", invocation.Status)
	}
	if invocation.Error == nil || !strings.Contains(invocation.Error.Message, "artifact count quota exceeded") {
		t.Fatalf("expected artifact count quota message, got %#v", invocation.Error)
	}
}

func TestInvokeTool_DeniesWhenArtifactSizeQuotaExceeded(t *testing.T) {
	t.Setenv("WORKSPACE_MAX_ARTIFACT_BYTES_PER_INVOCATION", "4")

	session := defaultSession()
	capability := defaultCapability()
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Artifacts: []ArtifactPayload{
			{Name: "a.txt", ContentType: "text/plain", Data: []byte("123")},
			{Name: "b.txt", ContentType: "text/plain", Data: []byte("45")},
		}}},
		&fakeArtifactStore{},
	)

	invocation, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{Args: json.RawMessage(`{}`)})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected artifact size quota policy denied, got %#v", err)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation status, got %#v", invocation.Status)
	}
	if invocation.Error == nil || !strings.Contains(invocation.Error.Message, "artifact size quota exceeded") {
		t.Fatalf("expected artifact size quota message, got %#v", invocation.Error)
	}
}

func TestInvokeToolExecutionBranches(t *testing.T) {
	session := defaultSession()
	capability := defaultCapability()

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{err: &domain.Error{Code: ErrorCodeExecutionFailed, Message: "failed"}, result: ToolRunResult{ExitCode: 2}},
		&fakeArtifactStore{},
	)
	_, err := svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeExecutionFailed {
		t.Fatalf("expected execution failed, got %#v", err)
	}

	capabilityTimeout := capability
	capabilityTimeout.Constraints.TimeoutSeconds = 0
	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capabilityTimeout.Name: capabilityTimeout}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{err: &domain.Error{Code: ErrorCodeTimeout, Message: "timeout"}},
		&fakeArtifactStore{},
	)
	_, err = svc.InvokeTool(context.Background(), "session-1", capabilityTimeout.Name, InvokeToolRequest{})
	if err == nil || err.HTTPStatus != 504 {
		t.Fatalf("expected timeout service error, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}}},
		&fakeArtifactStore{saveErr: errors.New("artifact save")},
	)
	_, err = svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal artifact error, got %#v", err)
	}

	svc = newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}, ExitCode: 0}},
		&fakeArtifactStore{saved: []domain.Artifact{{ID: "a1"}}},
	)
	invocation, err := svc.InvokeTool(context.Background(), "session-1", capability.Name, InvokeToolRequest{})
	if err != nil {
		t.Fatalf("unexpected invoke success error: %v", err)
	}
	if invocation.Status != domain.InvocationStatusSucceeded {
		t.Fatalf("expected succeeded invocation, got %s", invocation.Status)
	}
}

func TestInvokeToolDeniesClusterScopeWhenRuntimeIsNotKubernetes(t *testing.T) {
	session := defaultSession()
	capability := domain.Capability{
		Name:          "k8s.get_pods",
		Scope:         domain.ScopeCluster,
		Observability: domain.Observability{TraceName: "trace", SpanName: "span"},
	}
	tool := &fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}, ExitCode: 0}}

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		tool,
		&fakeArtifactStore{},
	)
	invocation, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected runtime policy denied, got %#v", err)
	}
	if invocation.Status != domain.InvocationStatusDenied {
		t.Fatalf("expected denied invocation, got %s", invocation.Status)
	}
	if invocation.Error == nil || invocation.Error.Message != "tool requires kubernetes runtime" {
		t.Fatalf("unexpected runtime deny message: %#v", invocation.Error)
	}
	if tool.calls != 0 {
		t.Fatalf("expected tool engine not to be called, got %d calls", tool.calls)
	}
}

func TestGetInvocationAndArtifactsBranches(t *testing.T) {
	capability := defaultCapability()
	session := defaultSession()

	store := &fakeArtifactStore{listed: []domain.Artifact{{ID: "list-1"}}}
	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}}},
		store,
	)

	_, err := svc.GetInvocation(context.Background(), "missing")
	if err == nil || err.Code != ErrorCodeNotFound {
		t.Fatalf("expected missing invocation error, got %#v", err)
	}

	invocation, invokeErr := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{})
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr)
	}

	logs, err := svc.GetInvocationLogs(context.Background(), invocation.ID)
	if err != nil {
		t.Fatalf("unexpected logs error: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected empty logs, got %#v", logs)
	}

	artifacts, err := svc.GetInvocationArtifacts(context.Background(), invocation.ID)
	if err != nil {
		t.Fatalf("unexpected artifacts error: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != "list-1" {
		t.Fatalf("unexpected artifacts list: %#v", artifacts)
	}

	svcWithListError := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}}},
		&fakeArtifactStore{listErr: errors.New("list failed")},
	)
	invocation2, invokeErr := svcWithListError.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{})
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr)
	}
	_, err = svcWithListError.GetInvocationArtifacts(context.Background(), invocation2.ID)
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected list internal error, got %#v", err)
	}
}

func TestPolicyDeniedDefaultCodeAndAuditEventHelper(t *testing.T) {
	session := defaultSession()
	capability := defaultCapability()
	invocation := domain.Invocation{ID: "inv-1", ToolName: capability.Name}
	_ = auditEventFromInvocation(session, invocation)

	svc := newServiceForTest(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: false, Reason: "denied"}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
	)
	_, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodePolicyDenied {
		t.Fatalf("expected default policy denied code, got %#v", err)
	}
}

func TestInvokeToolFailsWhenInvocationStoreSaveFails(t *testing.T) {
	session := defaultSession()
	capability := defaultCapability()
	store := &fakeInvocationStore{saveErr: errors.New("store down")}

	svc := NewService(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		&fakeToolEngine{},
		&fakeArtifactStore{},
		&fakeAudit{},
		store,
	)

	_, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{})
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal error on invocation store save failure, got %#v", err)
	}
}

func TestGetInvocationFailsWhenInvocationStoreGetFails(t *testing.T) {
	store := &fakeInvocationStore{getErr: errors.New("read failure")}
	svc := NewService(
		&fakeWorkspaceManager{},
		&fakeCatalog{},
		&fakePolicyEngine{},
		&fakeToolEngine{},
		&fakeArtifactStore{},
		&fakeAudit{},
		store,
	)

	_, err := svc.GetInvocation(context.Background(), "inv-1")
	if err == nil || err.Code != ErrorCodeInternal {
		t.Fatalf("expected internal error on invocation store get failure, got %#v", err)
	}
}

func TestInvokeTool_DeduplicatesByCorrelationID(t *testing.T) {
	session := defaultSession()
	capability := defaultCapability()
	tool := &fakeToolEngine{result: ToolRunResult{Output: map[string]any{"ok": true}, ExitCode: 0}}

	svc := NewService(
		&fakeWorkspaceManager{session: session, found: true},
		&fakeCatalog{entries: map[string]domain.Capability{capability.Name: capability}},
		&fakePolicyEngine{decision: PolicyDecision{Allow: true}},
		tool,
		&fakeArtifactStore{},
		&fakeAudit{},
		NewInMemoryInvocationStore(),
	)

	first, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("unexpected first invoke error: %v", err)
	}
	second, err := svc.InvokeTool(context.Background(), session.ID, capability.Name, InvokeToolRequest{
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("unexpected second invoke error: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected dedupe to return same invocation id, got %s vs %s", first.ID, second.ID)
	}
	if tool.calls != 1 {
		t.Fatalf("expected tool to run once, got %d", tool.calls)
	}
}

func TestGetInvocation_HydratesOutputAndLogsFromArtifactRefs(t *testing.T) {
	invocation := domain.Invocation{
		ID:        "inv-1",
		SessionID: "session-1",
		ToolName:  "fs.read",
		Status:    domain.InvocationStatusSucceeded,
		StartedAt: time.Now().UTC(),
		OutputRef: "artifact-out",
		LogsRef:   "artifact-log",
		Artifacts: []domain.Artifact{
			{ID: "artifact-out", Path: "/tmp/out.json"},
			{ID: "artifact-log", Path: "/tmp/log.jsonl"},
		},
	}
	store := &fakeInvocationStore{
		data: map[string]domain.Invocation{
			invocation.ID: invocation,
		},
	}
	artifactStore := &fakeArtifactStore{
		readByPath: map[string][]byte{
			"/tmp/out.json":  []byte(`{"ok":true}`),
			"/tmp/log.jsonl": []byte(`{"at":"2026-01-01T00:00:00Z","channel":"stdout","message":"hello"}` + "\n"),
		},
	}
	svc := NewService(
		&fakeWorkspaceManager{},
		&fakeCatalog{},
		&fakePolicyEngine{},
		&fakeToolEngine{},
		artifactStore,
		&fakeAudit{},
		store,
	)

	hydrated, err := svc.GetInvocation(context.Background(), invocation.ID)
	if err != nil {
		t.Fatalf("unexpected get invocation error: %v", err)
	}
	if hydrated.Output == nil {
		t.Fatalf("expected output to be hydrated from artifact ref")
	}

	logs, logsErr := svc.GetInvocationLogs(context.Background(), invocation.ID)
	if logsErr != nil {
		t.Fatalf("unexpected get invocation logs error: %v", logsErr)
	}
	if len(logs) != 1 || logs[0].Message != "hello" {
		t.Fatalf("expected hydrated logs from artifact ref, got %#v", logs)
	}
}
