package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// DockerCommandRunner executes commands inside Docker containers via exec.
type DockerCommandRunner struct {
	client workspaceadapter.DockerClient
}

// NewDockerCommandRunner creates a command runner that uses Docker exec.
func NewDockerCommandRunner(client workspaceadapter.DockerClient) *DockerCommandRunner {
	return &DockerCommandRunner{client: client}
}

func (r *DockerCommandRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if r.client == nil {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("docker client is required")
	}

	containerID := session.Runtime.ContainerID
	if containerID == "" {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("docker runtime container_id is required")
	}

	workdir := strings.TrimSpace(spec.Cwd)
	if workdir == "" {
		workdir = strings.TrimSpace(session.Runtime.Workdir)
	}
	if workdir == "" {
		workdir = strings.TrimSpace(session.WorkspacePath)
	}

	cmd := []string{"sh", "-c", buildShellCommand(workdir, spec.Command, spec.Args)}

	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  len(spec.Stdin) > 0,
	}

	execResp, err := r.client.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("exec create: %w", err)
	}

	attachResp, err := r.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	if len(spec.Stdin) > 0 {
		_, _ = attachResp.Conn.Write(spec.Stdin)
		_ = attachResp.CloseWrite()
	}

	var stdout, stderr bytes.Buffer
	_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)

	output := combineOutput(stdout.Bytes(), stderr.Bytes())
	output = truncate(output, spec.MaxBytes)
	text := string(output)

	if copyErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return app.CommandResult{Output: text, ExitCode: 124}, fmt.Errorf("timeout: %w", ctx.Err())
		}
		return app.CommandResult{Output: text, ExitCode: -1}, fmt.Errorf("read output: %w", copyErr)
	}

	inspected, err := r.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return app.CommandResult{Output: text, ExitCode: -1}, fmt.Errorf("exec inspect: %w", err)
	}

	if inspected.ExitCode != 0 {
		return app.CommandResult{
			Output:   text,
			ExitCode: inspected.ExitCode,
		}, fmt.Errorf("command exited %d", inspected.ExitCode)
	}

	return app.CommandResult{Output: text, ExitCode: 0}, nil
}
