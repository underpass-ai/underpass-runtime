package workspace

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

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
		execCreateID: "exec-1",
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	mgr := NewDockerManager(DockerManagerConfig{
		DefaultImage: "golang:1.25",
		Workdir:      "/workspace/repo",
		TTL:          time.Hour,
	}, client)

	session, err := mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "test-session",
		Principal: domain.Principal{TenantID: "tenant-1", ActorID: "actor-1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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

func TestDockerManager_ResourceLimits(t *testing.T) {
	client := &fakeDockerClient{createID: "limited"}
	mgr := NewDockerManager(DockerManagerConfig{
		CPULimit:    2,
		MemoryLimit: 1024 * 1024 * 1024,
	}, client)

	_, _ = mgr.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID: "limited-session",
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "t1"},
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
		Principal: domain.Principal{TenantID: "my-tenant"},
	})

	labels := client.createdConfig.Labels
	if labels["managed-by"] != "underpass-runtime" {
		t.Fatalf("expected managed-by label, got %v", labels)
	}
	if labels["session-id"] != "label-session" {
		t.Fatalf("expected session-id label, got %v", labels)
	}
	if labels["tenant-id"] != "my-tenant" {
		t.Fatalf("expected tenant-id label, got %v", labels)
	}
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
