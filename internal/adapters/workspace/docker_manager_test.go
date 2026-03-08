package workspace

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const testExecID = "exec-1"

type fakeDockerClient struct {
	createID      string
	createErr     error
	startErr      error
	inspectResp   container.InspectResponse
	inspectErr    error
	stopErr       error
	removeErr     error
	execCreateID  string
	execCreateErr error
	execAttachErr error
	execInspect   container.ExecInspect
	execInspErr   error

	createdConfig *container.Config
	createdHost   *container.HostConfig
	createdNet    *network.NetworkingConfig
	createdName   string
	startedID     string
	stoppedID     string
	removedID     string
	removedForce  bool
}

func (f *fakeDockerClient) ContainerCreate(_ context.Context, config *container.Config, hostConfig *container.HostConfig, netConfig *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	f.createdConfig = config
	f.createdHost = hostConfig
	f.createdNet = netConfig
	f.createdName = name
	return container.CreateResponse{ID: f.createID}, f.createErr
}

func (f *fakeDockerClient) ContainerStart(_ context.Context, id string, _ container.StartOptions) error {
	f.startedID = id
	return f.startErr
}

func (f *fakeDockerClient) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return f.inspectResp, f.inspectErr
}

func (f *fakeDockerClient) ContainerStop(_ context.Context, id string, _ container.StopOptions) error {
	f.stoppedID = id
	return f.stopErr
}

func (f *fakeDockerClient) ContainerRemove(_ context.Context, id string, opts container.RemoveOptions) error {
	f.removedID = id
	f.removedForce = opts.Force
	return f.removeErr
}

func (f *fakeDockerClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return nil, nil
}
func (f *fakeDockerClient) ContainerLogs(_ context.Context, _ string, _ container.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *fakeDockerClient) ContainerExecCreate(_ context.Context, _ string, _ container.ExecOptions) (container.ExecCreateResponse, error) {
	return container.ExecCreateResponse{ID: f.execCreateID}, f.execCreateErr
}

func (f *fakeDockerClient) ContainerExecAttach(_ context.Context, _ string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
	if f.execAttachErr != nil {
		return types.HijackedResponse{}, f.execAttachErr
	}
	return types.NewHijackedResponse(newNoopConn(), "application/vnd.docker.raw-stream"), nil
}

func (f *fakeDockerClient) ContainerExecInspect(_ context.Context, _ string) (container.ExecInspect, error) {
	return f.execInspect, f.execInspErr
}

func TestDockerManager_CreateSession(t *testing.T) {
	client := &fakeDockerClient{
		createID:     "abc123",
		execCreateID: testExecID,
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	mgr := NewDockerManager(DockerManagerConfig{
		DefaultImage: "golang:1.25",
		Workdir:      "/workspace/repo",
		TTL:          time.Hour,
	}, client)

	session, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "test-session",
		Principal: domain.Principal{TenantID: testTenantID, ActorID: "actor-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.ID != "test-session" {
		t.Fatalf("expected session ID 'test-session', got %q", session.ID)
	}
	if session.Runtime.Kind != domain.RuntimeKindDocker {
		t.Fatalf("expected docker runtime, got %q", session.Runtime.Kind)
	}
	if session.Runtime.ContainerID != "abc123" {
		t.Fatalf("expected container ID 'abc123', got %q", session.Runtime.ContainerID)
	}
	if client.createdConfig.Image != "golang:1.25" {
		t.Fatalf("expected image 'golang:1.25', got %q", client.createdConfig.Image)
	}
	if client.startedID != "abc123" {
		t.Fatalf("expected start on 'abc123', got %q", client.startedID)
	}
}

func TestDockerManager_CreateSession_WithRepoURL(t *testing.T) {
	client := &fakeDockerClient{
		createID:     "repo-container",
		execCreateID: "exec-clone",
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	mgr := NewDockerManager(DockerManagerConfig{}, client)

	session, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "repo-session",
		RepoURL:   "https://github.com/example/repo.git",
		RepoRef:   "main",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.RepoURL != "https://github.com/example/repo.git" {
		t.Fatalf("expected repo URL in session")
	}
}

func TestDockerManager_CreateSession_CreateError(t *testing.T) {
	client := &fakeDockerClient{createErr: fmt.Errorf("docker unavailable")}
	mgr := NewDockerManager(DockerManagerConfig{}, client)

	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "fail-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDockerManager_CreateSession_StartError(t *testing.T) {
	client := &fakeDockerClient{
		createID: "start-fail",
		startErr: fmt.Errorf("port conflict"),
	}
	mgr := NewDockerManager(DockerManagerConfig{}, client)

	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "start-fail-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected start error")
	}
	if client.removedID != "start-fail" {
		t.Fatalf("expected cleanup remove on start failure, got %q", client.removedID)
	}
}

func TestDockerManager_CreateSession_NilClient(t *testing.T) {
	mgr := NewDockerManager(DockerManagerConfig{}, nil)
	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "nil-client",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestDockerManager_GetSession_InMemory(t *testing.T) {
	client := &fakeDockerClient{
		createID: "get-container",
		inspectResp: container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				State: &container.State{Running: true},
			},
		},
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "get-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	session, ok, err := mgr.GetSession(context.Background(), "get-session")
	if err != nil || !ok {
		t.Fatalf("expected session found, got ok=%v err=%v", ok, err)
	}
	if session.Runtime.ContainerID != "get-container" {
		t.Fatalf("expected container ID 'get-container', got %q", session.Runtime.ContainerID)
	}
}

func TestDockerManager_GetSession_NotFound(t *testing.T) {
	mgr := NewDockerManager(DockerManagerConfig{}, &fakeDockerClient{})
	_, ok, err := mgr.GetSession(context.Background(), "nonexistent")
	if err != nil || ok {
		t.Fatalf("expected not found, got ok=%v err=%v", ok, err)
	}
}

func TestDockerManager_CloseSession(t *testing.T) {
	client := &fakeDockerClient{createID: "close-container"}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "close-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	err := mgr.CloseSession(context.Background(), "close-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.stoppedID != "close-container" {
		t.Fatalf("expected stop on 'close-container', got %q", client.stoppedID)
	}
	if client.removedID != "close-container" {
		t.Fatalf("expected remove on 'close-container', got %q", client.removedID)
	}
}

func TestDockerManager_CloseSession_NotFound(t *testing.T) {
	mgr := NewDockerManager(DockerManagerConfig{}, &fakeDockerClient{})
	err := mgr.CloseSession(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("expected no error for nonexistent session, got %v", err)
	}
}

func TestDockerManager_CloseSession_WithSessionStore(t *testing.T) {
	store := app.NewInMemorySessionStore()
	client := &fakeDockerClient{createID: "store-close"}
	mgr := NewDockerManager(DockerManagerConfig{
		TTL:          time.Hour,
		SessionStore: store,
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "store-close-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	err := mgr.CloseSession(context.Background(), "store-close-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.stoppedID != "store-close" {
		t.Fatalf("expected stop on container, got %q", client.stoppedID)
	}

	// verify session was deleted from store
	_, ok, _ := store.Get(context.Background(), "store-close-session")
	if ok {
		t.Fatal("expected session deleted from store")
	}
}

func TestDockerManager_CloseSession_StoreOnlyFallback(t *testing.T) {
	store := app.NewInMemorySessionStore()
	client := &fakeDockerClient{}

	// save a session directly in the store (simulating restart scenario)
	_ = store.Save(context.Background(), domain.Session{
		ID: "orphan-session",
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: "orphan-container",
		},
	})

	mgr := NewDockerManager(DockerManagerConfig{
		TTL:          time.Hour,
		SessionStore: store,
	}, client)

	err := mgr.CloseSession(context.Background(), "orphan-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.stoppedID != "orphan-container" {
		t.Fatalf("expected stop on orphan-container, got %q", client.stoppedID)
	}
	if client.removedID != "orphan-container" {
		t.Fatalf("expected remove on orphan-container, got %q", client.removedID)
	}
}

func TestDockerManager_ResourceLimits(t *testing.T) {
	client := &fakeDockerClient{createID: "limited"}
	mgr := NewDockerManager(DockerManagerConfig{
		CPULimit:    2,
		MemoryLimit: 1024 * 1024 * 1024,
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "limited-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	if client.createdHost.NanoCPUs != 2e9 {
		t.Fatalf("expected 2e9 nanocpus, got %d", client.createdHost.NanoCPUs)
	}
	if client.createdHost.Memory != 1024*1024*1024 {
		t.Fatalf("expected 1GiB memory, got %d", client.createdHost.Memory)
	}
}

func TestDockerManager_ImageResolution(t *testing.T) {
	client := &fakeDockerClient{createID: "img-test"}
	mgr := NewDockerManager(DockerManagerConfig{
		DefaultImage: "default:latest",
		ImageBundles: map[string]string{
			"go":   "golang:1.25",
			"node": "node:22",
		},
		ProfileKey: "runner_profile",
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "profile-session",
		Principal: domain.Principal{TenantID: testTenantID},
		Metadata:  map[string]string{"runner_profile": "go"},
	})

	if client.createdConfig.Image != "golang:1.25" {
		t.Fatalf("expected 'golang:1.25', got %q", client.createdConfig.Image)
	}
}

func TestDockerManager_ImageResolution_Default(t *testing.T) {
	client := &fakeDockerClient{createID: "img-default"}
	mgr := NewDockerManager(DockerManagerConfig{
		DefaultImage: "custom:latest",
		ImageBundles: map[string]string{"go": "golang:1.25"},
		ProfileKey:   "runner_profile",
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "default-image-session",
		Principal: domain.Principal{TenantID: testTenantID},
		Metadata:  map[string]string{"runner_profile": "unknown"},
	})

	if client.createdConfig.Image != "custom:latest" {
		t.Fatalf("expected default image, got %q", client.createdConfig.Image)
	}
}

func TestDockerManager_ContainerName(t *testing.T) {
	mgr := &DockerManager{cfg: DockerManagerConfig{ContainerPrefix: "ws"}}

	tests := []struct {
		sessionID string
		expected  string
	}{
		{"session-abc", "ws-session-abc"},
		{"SESSION-ABC", "ws-session-abc"},
		{"a.b/c:d", "ws-a-b-c-d"},
	}
	for _, tt := range tests {
		got := mgr.containerName(tt.sessionID)
		if got != tt.expected {
			t.Errorf("containerName(%q) = %q, want %q", tt.sessionID, got, tt.expected)
		}
	}
}

func TestDockerManager_ContainerNameLong(t *testing.T) {
	mgr := &DockerManager{cfg: DockerManagerConfig{ContainerPrefix: "ws"}}
	long := "session-" + string(make([]byte, 100))
	name := mgr.containerName(long)
	if len(name) > 63 {
		t.Fatalf("expected name <= 63 chars, got %d", len(name))
	}
}

func TestDockerManager_Network(t *testing.T) {
	client := &fakeDockerClient{createID: "net-test"}
	mgr := NewDockerManager(DockerManagerConfig{
		Network: "workspace-net",
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "net-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	if client.createdNet == nil {
		t.Fatal("expected network config")
	}
	if _, ok := client.createdNet.EndpointsConfig["workspace-net"]; !ok {
		t.Fatal("expected workspace-net endpoint")
	}
}

func TestDockerManager_GetSession_WithSessionStore(t *testing.T) {
	store := app.NewInMemorySessionStore()
	client := &fakeDockerClient{createID: "store-container"}
	mgr := NewDockerManager(DockerManagerConfig{
		TTL:          time.Hour,
		SessionStore: store,
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "store-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	session, ok, err := mgr.GetSession(context.Background(), "store-session")
	if err != nil || !ok {
		t.Fatalf("expected session found via store, got ok=%v err=%v", ok, err)
	}
	if session.ID != "store-session" {
		t.Fatalf("expected session ID 'store-session', got %q", session.ID)
	}
}

func TestDockerManager_Labels(t *testing.T) {
	client := &fakeDockerClient{createID: "label-test"}
	mgr := NewDockerManager(DockerManagerConfig{}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "label-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	labels := client.createdConfig.Labels
	if labels[labelManagedBy] != labelRuntimeValue {
		t.Fatalf("expected %s label, got %v", labelManagedBy, labels)
	}
	if labels[labelSessionID] != "label-session" {
		t.Fatalf("expected %s label, got %v", labelSessionID, labels)
	}
	if labels[labelTenantID] != testTenantID {
		t.Fatalf("expected %s label, got %v", labelTenantID, labels)
	}
}

func TestDockerManager_GetSession_Expired(t *testing.T) {
	store := app.NewInMemorySessionStore()
	client := &fakeDockerClient{createID: "expired-container"}
	mgr := NewDockerManager(DockerManagerConfig{
		TTL:          1 * time.Millisecond,
		SessionStore: store,
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID:       "expired-session",
		Principal:       domain.Principal{TenantID: testTenantID},
		ExpiresInSecond: 0,
	})

	time.Sleep(5 * time.Millisecond)

	_, ok, err := mgr.GetSession(context.Background(), "expired-session")
	if err != nil || ok {
		t.Fatalf("expected expired session not found, got ok=%v err=%v", ok, err)
	}
}

func TestDockerManager_GetSession_NotRunning(t *testing.T) {
	client := &fakeDockerClient{
		createID: "stopped-container",
		inspectResp: container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				State: &container.State{Running: false},
			},
		},
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "stopped-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	_, ok, _ := mgr.GetSession(context.Background(), "stopped-session")
	if ok {
		t.Fatal("expected not-running container to return not found")
	}
}

func TestDockerManager_GetSession_InspectError(t *testing.T) {
	client := &fakeDockerClient{
		createID:   "inspect-err",
		inspectErr: fmt.Errorf("not found"),
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "inspect-err-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})

	_, ok, err := mgr.GetSession(context.Background(), "inspect-err-session")
	if ok {
		t.Fatal("expected not found on inspect error")
	}
	if err == nil {
		t.Fatal("expected error propagated from inspect")
	}
}

func TestDockerManager_CloneRepoError(t *testing.T) {
	client := &fakeDockerClient{
		createID:      "clone-err",
		execCreateErr: fmt.Errorf("exec unavailable"),
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "clone-err-session",
		RepoURL:   "https://example.com/repo.git",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected clone error")
	}
	if client.removedID != "clone-err" {
		t.Fatalf("expected cleanup on clone failure, got removed=%q", client.removedID)
	}
}

func TestDockerManager_CloneRepoNonZeroExit(t *testing.T) {
	client := &fakeDockerClient{
		createID:     "clone-nz",
		execCreateID: testExecID,
		execInspect:  container.ExecInspect{ExitCode: 128},
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "clone-nz-session",
		RepoURL:   "https://example.com/repo.git",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected error for non-zero clone exit")
	}
}

func TestDockerManager_SessionStoreSaveError(t *testing.T) {
	store := &failingSessionStore{saveErr: fmt.Errorf("save failed")}
	client := &fakeDockerClient{createID: "save-err"}
	mgr := NewDockerManager(DockerManagerConfig{
		TTL:          time.Hour,
		SessionStore: store,
	}, client)

	_, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "save-err-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err == nil {
		t.Fatal("expected session store save error")
	}
	if client.removedID != "save-err" {
		t.Fatalf("expected cleanup on save failure, got removed=%q", client.removedID)
	}
}

func TestDockerManager_CreateSession_GenerateID(t *testing.T) {
	client := &fakeDockerClient{createID: "gen-id"}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	session, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: testTenantID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.ID == "" {
		t.Fatal("expected generated session ID")
	}
}

func TestDockerManager_CreateSession_CustomExpiry(t *testing.T) {
	client := &fakeDockerClient{createID: "custom-exp"}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)

	session, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID:       "custom-exp-session",
		ExpiresInSecond: 120,
		Principal:       domain.Principal{TenantID: testTenantID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedMax := time.Now().Add(130 * time.Second)
	if session.ExpiresAt.After(expectedMax) {
		t.Fatalf("custom expiry not applied, got %v", session.ExpiresAt)
	}
}

func TestDockerManager_GetSession_NilState(t *testing.T) {
	client := &fakeDockerClient{
		createID:    "nil-state",
		inspectResp: container.InspectResponse{},
	}
	mgr := NewDockerManager(DockerManagerConfig{TTL: time.Hour}, client)
	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "nil-state-session",
		Principal: domain.Principal{TenantID: testTenantID},
	})
	_, ok, _ := mgr.GetSession(context.Background(), "nil-state-session")
	if ok {
		t.Fatal("expected not found for nil state")
	}
}

type failingSessionStore struct {
	saveErr error
}

func (s *failingSessionStore) Save(_ context.Context, _ domain.Session) error {
	return s.saveErr
}
func (s *failingSessionStore) Get(_ context.Context, _ string) (domain.Session, bool, error) {
	return domain.Session{}, false, nil
}
func (s *failingSessionStore) Delete(_ context.Context, _ string) error {
	return nil
}

// noopConn implements net.Conn for faking HijackedResponse.
type noopConn struct{ io.Reader }

func newNoopConn() *noopConn                    { return &noopConn{Reader: io.NopCloser(emptyReader{})} }
func (c *noopConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *noopConn) Close() error                { return nil }
func (c *noopConn) Read(b []byte) (int, error)  { return 0, io.EOF }

func (c *noopConn) LocalAddr() net.Addr                { return noopAddr{} }
func (c *noopConn) RemoteAddr() net.Addr               { return noopAddr{} }
func (c *noopConn) SetDeadline(_ time.Time) error      { return nil }
func (c *noopConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *noopConn) SetWriteDeadline(_ time.Time) error { return nil }

type noopAddr struct{}

func (noopAddr) Network() string { return "tcp" }
func (noopAddr) String() string  { return "127.0.0.1:0" }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
