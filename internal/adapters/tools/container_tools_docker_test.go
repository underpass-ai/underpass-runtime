package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testDockerContainerID = "abc123def456"
	testDockerImage       = "alpine:3.20"
)

// fakeDockerOpsClient implements workspace.DockerClient for container_tools_docker tests.
type fakeDockerOpsClient struct {
	listResult  []container.Summary
	listErr     error
	logsData    string
	logsErr     error
	createID    string
	createErr   error
	startErr    error
	inspectResp container.InspectResponse
	inspectErr  error
	stopErr     error
	removeErr   error

	execCreateID  string
	execCreateErr error
	execAttachErr error
	execInspect   container.ExecInspect
	execInspErr   error
	execStdout    []byte
	execStderr    []byte
}

func (f *fakeDockerOpsClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	return container.CreateResponse{ID: f.createID}, f.createErr
}
func (f *fakeDockerOpsClient) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return f.startErr
}
func (f *fakeDockerOpsClient) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return f.inspectResp, f.inspectErr
}
func (f *fakeDockerOpsClient) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	return f.stopErr
}
func (f *fakeDockerOpsClient) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	return f.removeErr
}
func (f *fakeDockerOpsClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.listResult, f.listErr
}
func (f *fakeDockerOpsClient) ContainerLogs(_ context.Context, _ string, _ container.LogsOptions) (io.ReadCloser, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	// Build multiplexed stream with stdout frame
	var buf bytes.Buffer
	if f.logsData != "" {
		data := []byte(f.logsData)
		header := make([]byte, 8)
		header[0] = 1 // stdout
		binary.BigEndian.PutUint32(header[4:], uint32(len(data)))
		buf.Write(header)
		buf.Write(data)
	}
	return io.NopCloser(&buf), nil
}
func (f *fakeDockerOpsClient) ContainerExecCreate(_ context.Context, _ string, _ container.ExecOptions) (container.ExecCreateResponse, error) {
	return container.ExecCreateResponse{ID: f.execCreateID}, f.execCreateErr
}
func (f *fakeDockerOpsClient) ContainerExecAttach(_ context.Context, _ string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
	if f.execAttachErr != nil {
		return types.HijackedResponse{}, f.execAttachErr
	}
	conn := newFakeDockerConn(f.execStdout, f.execStderr)
	return types.NewHijackedResponse(conn, "application/vnd.docker.multiplexed-stream"), nil
}
func (f *fakeDockerOpsClient) ContainerExecInspect(_ context.Context, _ string) (container.ExecInspect, error) {
	return f.execInspect, f.execInspErr
}

func dockerSession() domain.Session {
	return domain.Session{
		ID: "docker-sess-1",
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: testDockerContainerID,
		},
	}
}

func TestContainerDockerAdapter_PS(t *testing.T) {
	client := &fakeDockerOpsClient{
		listResult: []container.Summary{
			{ID: "bbb123456789ab", Image: "nginx:latest", Names: []string{"/ws-session-b"}, Status: "Up 5 minutes"},
			{ID: "aaa123456789ab", Image: testDockerImage, Names: []string{"/ws-session-a"}, Status: "Up 10 minutes"},
		},
	}
	handler := NewContainerPSHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceRuntime] != containerDockerRuntime {
		t.Fatalf("expected docker runtime, got %v", output[containerSourceRuntime])
	}
	if output["count"] != 2 {
		t.Fatalf("expected 2 containers, got %v", output["count"])
	}
	containers := output["containers"].([]map[string]any)
	if containers[0]["id"] != "aaa123456789" {
		t.Fatalf("expected sorted by ID, first got %v", containers[0]["id"])
	}
}

func TestContainerDockerAdapter_PS_Truncation(t *testing.T) {
	client := &fakeDockerOpsClient{
		listResult: []container.Summary{
			{ID: "aaa123456789ab", Image: testDockerImage, Names: []string{"/a"}, Status: "Up"},
			{ID: "bbb123456789ab", Image: testDockerImage, Names: []string{"/b"}, Status: "Up"},
			{ID: "ccc123456789ab", Image: testDockerImage, Names: []string{"/c"}, Status: "Up"},
		},
	}
	handler := NewContainerPSHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"limit":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 2 {
		t.Fatalf("expected truncated to 2, got %v", output["count"])
	}
	if output[containerKeyTruncated] != true {
		t.Fatal("expected truncated=true")
	}
}

func TestContainerDockerAdapter_PS_ListError(t *testing.T) {
	client := &fakeDockerOpsClient{listErr: fmt.Errorf("connection refused")}
	handler := NewContainerPSHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestContainerDockerAdapter_PS_NilClient(t *testing.T) {
	adapter := &containerDockerAdapter{client: nil}
	_, err := adapter.invokePS(context.Background(), dockerSession(), false, 50, "")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestContainerDockerAdapter_Logs(t *testing.T) {
	client := &fakeDockerOpsClient{logsData: "line 1\nline 2\n"}
	handler := NewContainerLogsHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceRuntime] != containerDockerRuntime {
		t.Fatalf("expected docker runtime, got %v", output[containerSourceRuntime])
	}
	if !strings.Contains(output["logs"].(string), "line 1") {
		t.Fatalf("expected logs to contain 'line 1', got %v", output["logs"])
	}
}

func TestContainerDockerAdapter_Logs_Error(t *testing.T) {
	client := &fakeDockerOpsClient{logsErr: fmt.Errorf("no such container")}
	handler := NewContainerLogsHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestContainerDockerAdapter_Run(t *testing.T) {
	client := &fakeDockerOpsClient{
		createID: "a1b2c3d4e5f6789012345678",
		inspectResp: container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				State: &container.State{Running: true},
			},
		},
	}
	handler := NewContainerRunHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"image_ref":"alpine:3.20","command":["echo","hi"],"detach":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceRuntime] != containerDockerRuntime {
		t.Fatalf("expected docker runtime, got %v", output[containerSourceRuntime])
	}
	if output[containerKeyStatus] != "running" {
		t.Fatalf("expected status=running, got %v", output[containerKeyStatus])
	}
	if output[containerKeyContainerID] != "a1b2c3d4e5f6" {
		t.Fatalf("expected truncated container ID 'a1b2c3d4e5f6', got %v", output[containerKeyContainerID])
	}
}

func TestContainerDockerAdapter_Run_NonDetach(t *testing.T) {
	client := &fakeDockerOpsClient{
		createID: "a1b2c3d4e5f6789012345678",
		inspectResp: container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				State: &container.State{Running: false, ExitCode: 0},
			},
		},
	}
	handler := NewContainerRunHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"image_ref":"alpine:3.20","command":["echo","done"],"detach":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "exited" {
		t.Fatalf("expected status=exited, got %v", output[containerKeyStatus])
	}
	if output[containerKeyExitCode] != 0 {
		t.Fatalf("expected exit code 0, got %v", output[containerKeyExitCode])
	}
}

func TestContainerDockerAdapter_Run_CreateError(t *testing.T) {
	client := &fakeDockerOpsClient{createErr: fmt.Errorf("image not found")}
	handler := NewContainerRunHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"image_ref":"nonexistent:latest"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestContainerDockerAdapter_Run_StartError(t *testing.T) {
	client := &fakeDockerOpsClient{
		createID: "start-fail-123456",
		startErr: fmt.Errorf("port conflict"),
	}
	handler := NewContainerRunHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"image_ref":"alpine:3.20"}`))
	if err == nil {
		t.Fatal("expected error on start failure")
	}
}

func TestContainerDockerAdapter_Exec(t *testing.T) {
	client := &fakeDockerOpsClient{
		execCreateID: testExecID,
		execStdout:   []byte("hello from exec"),
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	handler := NewContainerExecHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456","command":["echo","hello"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceRuntime] != containerDockerRuntime {
		t.Fatalf("expected docker runtime, got %v", output[containerSourceRuntime])
	}
	if !strings.Contains(output[containerKeyOutput].(string), "hello from exec") {
		t.Fatalf("expected exec output, got %v", output[containerKeyOutput])
	}
	if output[containerKeyExitCode] != 0 {
		t.Fatalf("expected exit code 0, got %v", output[containerKeyExitCode])
	}
}

func TestContainerDockerAdapter_Exec_NonZeroExit(t *testing.T) {
	client := &fakeDockerOpsClient{
		execCreateID: testExecID,
		execStderr:   []byte("command not found"),
		execInspect:  container.ExecInspect{ExitCode: 127},
	}
	handler := NewContainerExecHandlerWithDocker(nil, client)
	result, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456","command":["echo","test"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := result.Output.(map[string]any)
	if output[containerKeyExitCode] != 127 {
		t.Fatalf("expected exit code 127, got %v", output[containerKeyExitCode])
	}
	if output[containerKeySummary] != "container exec failed" {
		t.Fatalf("expected failed summary, got %v", output[containerKeySummary])
	}
}

func TestContainerDockerAdapter_Exec_CreateError(t *testing.T) {
	client := &fakeDockerOpsClient{execCreateErr: fmt.Errorf("container not running")}
	handler := NewContainerExecHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456","command":["echo","test"]}`))
	if err == nil {
		t.Fatal("expected exec create error")
	}
}

func TestContainerDockerAdapter_Exec_AttachError(t *testing.T) {
	client := &fakeDockerOpsClient{
		execCreateID:  testExecID,
		execAttachErr: fmt.Errorf("attach failed"),
	}
	handler := NewContainerExecHandlerWithDocker(nil, client)
	_, err := handler.Invoke(context.Background(), dockerSession(), []byte(`{"container_id":"abc123def456","command":["echo","test"]}`))
	if err == nil {
		t.Fatal("expected exec attach error")
	}
}
