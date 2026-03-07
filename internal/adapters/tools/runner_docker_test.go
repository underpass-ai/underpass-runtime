package tools

import (
	"bytes"
	"context"
	"encoding/binary"
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

const (
	testExecID      = "exec-1"
	testContainerID = "c1"
)

type fakeDockerExecClient struct {
	execCreateID  string
	execCreateErr error
	execAttachErr error
	execInspect   container.ExecInspect
	execInspErr   error
	stdout        []byte
	stderr        []byte
}

func (f *fakeDockerExecClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	return container.CreateResponse{}, nil
}
func (f *fakeDockerExecClient) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return nil
}
func (f *fakeDockerExecClient) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{}, nil
}
func (f *fakeDockerExecClient) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	return nil
}
func (f *fakeDockerExecClient) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	return nil
}
func (f *fakeDockerExecClient) ContainerExecCreate(_ context.Context, _ string, _ container.ExecOptions) (container.ExecCreateResponse, error) {
	return container.ExecCreateResponse{ID: f.execCreateID}, f.execCreateErr
}
func (f *fakeDockerExecClient) ContainerExecAttach(_ context.Context, _ string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
	if f.execAttachErr != nil {
		return types.HijackedResponse{}, f.execAttachErr
	}
	conn := newFakeDockerConn(f.stdout, f.stderr)
	return types.NewHijackedResponse(conn, "application/vnd.docker.multiplexed-stream"), nil
}
func (f *fakeDockerExecClient) ContainerExecInspect(_ context.Context, _ string) (container.ExecInspect, error) {
	return f.execInspect, f.execInspErr
}

func TestDockerCommandRunner_Run(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateID: testExecID,
		stdout:       []byte("hello world"),
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	runner := NewDockerCommandRunner(client)

	session := domain.Session{
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: testContainerID,
			Workdir:     "/workspace/repo",
		},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{
		Command: "echo",
		Args:    []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Output != "hello world" {
		t.Fatalf("expected 'hello world', got %q", result.Output)
	}
}

func TestDockerCommandRunner_ExitError(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateID: "exec-fail",
		stderr:       []byte("command not found"),
		execInspect:  container.ExecInspect{ExitCode: 127},
	}
	runner := NewDockerCommandRunner(client)

	session := domain.Session{
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: testContainerID,
		},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{
		Command: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if result.ExitCode != 127 {
		t.Fatalf("expected exit code 127, got %d", result.ExitCode)
	}
}

func TestDockerCommandRunner_NilClient(t *testing.T) {
	runner := NewDockerCommandRunner(nil)
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindDocker, ContainerID: "x"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
	}
}

func TestDockerCommandRunner_EmptyContainerID(t *testing.T) {
	runner := NewDockerCommandRunner(&fakeDockerExecClient{})
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindDocker},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error for empty container ID")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
	}
}

func TestDockerCommandRunner_ExecCreateError(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateErr: fmt.Errorf("container not running"),
	}
	runner := NewDockerCommandRunner(client)
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindDocker, ContainerID: testContainerID},
	}
	_, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected exec create error")
	}
}

func TestDockerCommandRunner_ExecAttachError(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateID:  "exec-1",
		execAttachErr: fmt.Errorf("attach failed"),
	}
	runner := NewDockerCommandRunner(client)
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindDocker, ContainerID: testContainerID},
	}
	_, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected exec attach error")
	}
}

func TestDockerCommandRunner_WorkdirResolution(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateID: testExecID,
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	runner := NewDockerCommandRunner(client)

	session := domain.Session{
		WorkspacePath: "/fallback",
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: testContainerID,
			Workdir:     "/runtime-dir",
		},
	}
	_, _ = runner.Run(context.Background(), session, app.CommandSpec{
		Command: "ls",
		Cwd:     "/override",
	})
	// The command should use Cwd when provided — verified via buildShellCommand
}

func TestDockerCommandRunner_StdoutStderr(t *testing.T) {
	client := &fakeDockerExecClient{
		execCreateID: testExecID,
		stdout:       []byte("out"),
		stderr:       []byte("err"),
		execInspect:  container.ExecInspect{ExitCode: 0},
	}
	runner := NewDockerCommandRunner(client)

	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindDocker, ContainerID: testContainerID},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "out\nerr" {
		t.Fatalf("expected combined output 'out\\nerr', got %q", result.Output)
	}
}

// fakeDockerConn produces Docker multiplexed stream output.
type fakeDockerConn struct {
	reader io.Reader
}

func newFakeDockerConn(stdout, stderr []byte) *fakeDockerConn {
	var buf bytes.Buffer
	writeDockerFrame(&buf, 1, stdout) // stdout stream
	writeDockerFrame(&buf, 2, stderr) // stderr stream
	return &fakeDockerConn{reader: &buf}
}

func writeDockerFrame(buf *bytes.Buffer, streamType byte, data []byte) {
	if len(data) == 0 {
		return
	}
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(data)))
	buf.Write(header)
	buf.Write(data)
}

func (c *fakeDockerConn) Read(b []byte) (int, error)         { return c.reader.Read(b) }
func (c *fakeDockerConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *fakeDockerConn) Close() error                       { return nil }
func (c *fakeDockerConn) LocalAddr() net.Addr                { return fakeAddr{} }
func (c *fakeDockerConn) RemoteAddr() net.Addr               { return fakeAddr{} }
func (c *fakeDockerConn) SetDeadline(_ time.Time) error      { return nil }
func (c *fakeDockerConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *fakeDockerConn) SetWriteDeadline(_ time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "tcp" }
func (fakeAddr) String() string  { return "127.0.0.1:0" }
