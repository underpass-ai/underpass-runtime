package tools

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"

	workspaceadapter "github.com/underpass-ai/underpass-runtime/internal/adapters/workspace"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	containerDockerRuntime = "docker"
	containerDockerLabel   = "managed-by"
	containerDockerValue   = "underpass-runtime"
)

// containerDockerAdapter implements containerDockerOps using the Docker Engine API.
type containerDockerAdapter struct {
	client workspaceadapter.DockerClient
}

func (a *containerDockerAdapter) invokePS(ctx context.Context, _ domain.Session, all bool, limit int, nameFilter string) (app.ToolRunResult, *domain.Error) {
	if a.client == nil {
		return app.ToolRunResult{}, dockerExecutionFailed("docker client is required")
	}

	opts := container.ListOptions{All: all}
	opts.Filters = filters.NewArgs(
		filters.Arg("label", containerDockerLabel+"="+containerDockerValue),
	)
	if nameFilter != "" {
		opts.Filters.Add("name", nameFilter)
	}

	containers, err := a.client.ContainerList(ctx, opts)
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker container list: %v", err))
	}

	entries := make([]map[string]any, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		entries = append(entries, map[string]any{
			"id":     c.ID[:12],
			"image":  c.Image,
			"name":   name,
			"status": c.Status,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return asString(entries[i]["id"]) < asString(entries[j]["id"])
	})

	truncated := false
	if len(entries) > limit {
		entries = entries[:limit]
		truncated = true
	}

	summary := fmt.Sprintf("listed %d containers", len(entries))
	output := map[string]any{
		containerSourceRuntime:   containerDockerRuntime,
		containerSourceSimulated: false,
		"all":                    all,
		"limit":                  limit,
		containerKeyNameFilter:   nameFilter,
		"count":                  len(entries),
		containerKeyTruncated:    truncated,
		"containers":             entries,
		containerKeySummary:      summary,
		containerKeyOutput:       summary,
		containerKeyExitCode:     0,
	}
	return containerResult(output, summary, containerPsReportJSON, containerPsOutputTxt), nil
}

func (a *containerDockerAdapter) invokeLogs(ctx context.Context, _ domain.Session, containerID string, tailLines, sinceSec int, timestamps bool, maxBytes int) (app.ToolRunResult, *domain.Error) {
	if a.client == nil {
		return app.ToolRunResult{}, dockerExecutionFailed("docker client is required")
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tailLines),
		Timestamps: timestamps,
	}
	if sinceSec > 0 {
		opts.Since = fmt.Sprintf("%ds", sinceSec)
	}

	reader, err := a.client.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker container logs: %v", err))
	}
	defer func() { _ = reader.Close() }()

	var stdout, stderr bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdout, &stderr, reader)
	logText := combineOutputStr(stdout.String(), stderr.String())

	output := buildContainerLogsOutput(containerLogsParams{
		runtime:     containerDockerRuntime,
		simulated:   false,
		containerID: containerID,
		tailLines:   tailLines,
		sinceSec:    sinceSec,
		timestamps:  timestamps,
		raw:         logText,
		maxBytes:    maxBytes,
		exitCode:    0,
	})
	return containerResult(output, logText, containerLogsReportJSON, containerLogsOutputTxt), nil
}

func (a *containerDockerAdapter) invokeRun(ctx context.Context, session domain.Session, imageRef string, command, envPairs []string, containerName string, detach, remove bool) (app.ToolRunResult, *domain.Error) {
	if a.client == nil {
		return app.ToolRunResult{}, dockerExecutionFailed("docker client is required")
	}

	env := make([]string, len(envPairs))
	copy(env, envPairs)

	labels := map[string]string{
		containerDockerLabel: containerDockerValue,
		"session-id":         session.ID,
	}

	cfg := &container.Config{
		Image:  imageRef,
		Env:    env,
		Labels: labels,
	}
	if len(command) > 0 {
		cfg.Cmd = command
	}

	hostCfg := &container.HostConfig{}
	if remove && detach {
		hostCfg.AutoRemove = true
	}

	var netCfg *network.NetworkingConfig
	resolvedName := containerName
	if resolvedName == "" {
		resolvedName = fmt.Sprintf("ws-run-%s-%d", sanitizeContainerLabelValue(session.ID), time.Now().UnixNano()%10000)
	}

	resp, err := a.client.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, resolvedName)
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker container create: %v", err))
	}

	if err := a.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = a.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker container start: %v", err))
	}

	cid := resp.ID
	if len(cid) > 12 {
		cid = cid[:12]
	}

	status := "running"
	exitCode := 0
	if !detach {
		status, exitCode = a.waitAndCollect(ctx, resp.ID, remove)
	}

	summary := fmt.Sprintf("container started: %s", cid)
	output := map[string]any{
		containerSourceRuntime:   containerDockerRuntime,
		containerSourceSimulated: false,
		"image_ref":              imageRef,
		"name":                   resolvedName,
		"detach":                 detach,
		"remove":                 remove,
		containerKeyCommand:      command,
		"env":                    envPairs,
		containerKeyContainerID:  cid,
		containerKeyStatus:       status,
		containerKeySummary:      summary,
		containerKeyOutput:       summary,
		containerKeyExitCode:     exitCode,
	}
	return containerResult(output, summary, containerRunReportJSON, containerRunOutputTxt), nil
}

func (a *containerDockerAdapter) waitAndCollect(ctx context.Context, containerID string, remove bool) (string, int) {
	deadline, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		info, err := a.client.ContainerInspect(deadline, containerID)
		if err != nil {
			return "unknown", -1
		}
		if info.ContainerJSONBase != nil && info.State != nil && !info.State.Running {
			exitCode := info.State.ExitCode
			if remove {
				_ = a.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			}
			return "exited", exitCode
		}
		select {
		case <-deadline.Done():
			return "running", 0
		case <-ticker.C:
		}
	}
}

func (a *containerDockerAdapter) invokeExec(ctx context.Context, _ domain.Session, containerID string, command []string, timeoutSec, maxBytes int) (app.ToolRunResult, *domain.Error) {
	if a.client == nil {
		return app.ToolRunResult{}, dockerExecutionFailed("docker client is required")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	execCfg := container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := a.client.ContainerExecCreate(timeoutCtx, containerID, execCfg)
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker exec create: %v", err))
	}

	attachResp, err := a.client.ContainerExecAttach(timeoutCtx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker exec attach: %v", err))
	}
	defer attachResp.Close()

	var stdout, stderr bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)

	outputBytes := combineOutput(stdout.Bytes(), stderr.Bytes())
	outputBytes = truncate(outputBytes, maxBytes)
	outputText := string(outputBytes)

	inspected, err := a.client.ContainerExecInspect(timeoutCtx, execResp.ID)
	if err != nil {
		return app.ToolRunResult{}, dockerExecutionFailed(fmt.Sprintf("docker exec inspect: %v", err))
	}

	summary := "container exec completed"
	if inspected.ExitCode != 0 {
		summary = "container exec failed"
	}

	output := map[string]any{
		containerSourceRuntime:     containerDockerRuntime,
		containerSourceSimulated:   false,
		containerKeyContainerID:    containerID,
		containerKeyCommand:        command,
		containerKeyTimeoutSeconds: timeoutSec,
		containerKeyExitCode:       inspected.ExitCode,
		containerKeySummary:        summary,
		containerKeyOutput:         outputText,
	}
	return containerResult(output, outputText, containerExecReportJSON, containerExecOutputTxt), nil
}

func combineOutputStr(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout == "" {
		return stderr
	}
	if stderr == "" {
		return stdout
	}
	return stdout + "\n" + stderr
}

func dockerExecutionFailed(message string) *domain.Error {
	return &domain.Error{
		Code:      app.ErrorCodeExecutionFailed,
		Message:   message,
		Retryable: true,
	}
}

// --- WithDocker constructors ---

func NewContainerPSHandlerWithDocker(runner app.CommandRunner, client workspaceadapter.DockerClient) *ContainerPSHandler {
	return &ContainerPSHandler{
		runner:    runner,
		dockerOps: &containerDockerAdapter{client: client},
	}
}

func NewContainerLogsHandlerWithDocker(runner app.CommandRunner, client workspaceadapter.DockerClient) *ContainerLogsHandler {
	return &ContainerLogsHandler{
		runner:    runner,
		dockerOps: &containerDockerAdapter{client: client},
	}
}

func NewContainerRunHandlerWithDocker(runner app.CommandRunner, client workspaceadapter.DockerClient) *ContainerRunHandler {
	return &ContainerRunHandler{
		runner:    runner,
		dockerOps: &containerDockerAdapter{client: client},
	}
}

func NewContainerExecHandlerWithDocker(runner app.CommandRunner, client workspaceadapter.DockerClient) *ContainerExecHandler {
	return &ContainerExecHandler{
		runner:    runner,
		dockerOps: &containerDockerAdapter{client: client},
	}
}
