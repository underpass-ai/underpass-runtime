package workspace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	labelManagedBy    = "managed-by"
	labelSessionID    = "session-id"
	labelTenantID     = "tenant-id"
	labelRuntimeValue = "underpass-runtime"

	defaultDockerImage     = "alpine:3.20"
	defaultWorkdir         = "/workspace/repo"
	defaultContainerPrefix = "ws"
	maxContainerNameLen    = 63
)

// DockerClient is a minimal interface over the Docker Engine API.
type DockerClient interface {
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error)
}

// DockerManagerConfig holds configuration for the Docker workspace backend.
type DockerManagerConfig struct {
	DefaultImage    string
	ImageBundles    map[string]string
	ProfileKey      string
	Workdir         string
	ContainerPrefix string
	Network         string
	CPULimit        int64
	MemoryLimit     int64
	TTL             time.Duration
	SessionStore    app.SessionStore
}

// DockerManager implements WorkspaceManager using Docker containers.
type DockerManager struct {
	cfg    DockerManagerConfig
	client DockerClient

	mu         sync.RWMutex
	containers map[string]string // sessionID → containerID
}

// NewDockerManager creates a workspace manager that uses Docker containers.
func NewDockerManager(cfg DockerManagerConfig, client DockerClient) *DockerManager {
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = defaultDockerImage
	}
	if cfg.Workdir == "" {
		cfg.Workdir = defaultWorkdir
	}
	if cfg.ContainerPrefix == "" {
		cfg.ContainerPrefix = defaultContainerPrefix
	}
	if cfg.TTL == 0 {
		cfg.TTL = time.Hour
	}
	return &DockerManager{
		cfg:        cfg,
		client:     client,
		containers: make(map[string]string),
	}
}

func (m *DockerManager) CreateSession(ctx context.Context, req app.CreateSessionRequest) (domain.Session, error) {
	if m.client == nil {
		return domain.Session{}, fmt.Errorf("docker client is required")
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = newSessionID()
	}

	image := m.resolveImage(req.Metadata)
	containerName := m.containerName(sessionID)

	containerCfg := &container.Config{
		Image:      image,
		Cmd:        []string{"sleep", "infinity"},
		WorkingDir: m.cfg.Workdir,
		Labels: map[string]string{
			labelManagedBy: labelRuntimeValue,
			labelSessionID: sessionID,
			labelTenantID:  req.Principal.TenantID,
		},
	}

	hostCfg := &container.HostConfig{}
	if m.cfg.CPULimit > 0 {
		hostCfg.NanoCPUs = m.cfg.CPULimit * 1e9
	}
	if m.cfg.MemoryLimit > 0 {
		hostCfg.Memory = m.cfg.MemoryLimit
	}

	var netCfg *network.NetworkingConfig
	if m.cfg.Network != "" {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				m.cfg.Network: {},
			},
		}
	}

	resp, err := m.client.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, containerName)
	if err != nil {
		return domain.Session{}, fmt.Errorf("create container: %w", err)
	}

	if err := m.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		if rmErr := m.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true}); rmErr != nil {
			slog.Warn("failed to remove container after start failure", "container", resp.ID, "error", rmErr)
		}
		return domain.Session{}, fmt.Errorf("start container: %w", err)
	}

	if req.RepoURL != "" {
		if err := m.cloneRepoInContainer(ctx, resp.ID, req.RepoURL, req.RepoRef); err != nil {
			if stopErr := m.client.ContainerStop(ctx, resp.ID, container.StopOptions{}); stopErr != nil {
				slog.Warn("failed to stop container after clone failure", "container", resp.ID, "error", stopErr)
			}
			if rmErr := m.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true}); rmErr != nil {
				slog.Warn("failed to remove container after clone failure", "container", resp.ID, "error", rmErr)
			}
			return domain.Session{}, fmt.Errorf("clone repo in container: %w", err)
		}
	}

	allowedPaths := req.AllowedPaths
	if len(allowedPaths) == 0 {
		allowedPaths = []string{"."}
	}

	now := time.Now().UTC()
	expiresAt := now.Add(m.cfg.TTL)
	if req.ExpiresInSecond > 0 {
		expiresAt = now.Add(time.Duration(req.ExpiresInSecond) * time.Second)
	}

	session := domain.Session{
		ID:            sessionID,
		WorkspacePath: m.cfg.Workdir,
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: resp.ID,
			Container:   containerName,
			Workdir:     m.cfg.Workdir,
		},
		RepoURL:      req.RepoURL,
		RepoRef:      req.RepoRef,
		AllowedPaths: allowedPaths,
		Principal:    req.Principal,
		Metadata:     req.Metadata,
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
	}

	m.mu.Lock()
	m.containers[sessionID] = resp.ID
	m.mu.Unlock()

	if m.cfg.SessionStore != nil {
		if err := m.cfg.SessionStore.Save(ctx, session); err != nil {
			if stopErr := m.client.ContainerStop(ctx, resp.ID, container.StopOptions{}); stopErr != nil {
				slog.Warn("failed to stop container after session save failure", "container", resp.ID, "error", stopErr)
			}
			if rmErr := m.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true}); rmErr != nil {
				slog.Warn("failed to remove container after session save failure", "container", resp.ID, "error", rmErr)
			}
			return domain.Session{}, fmt.Errorf("save session: %w", err)
		}
	}

	return session, nil
}

func (m *DockerManager) GetSession(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	if m.cfg.SessionStore != nil {
		session, ok, err := m.cfg.SessionStore.Get(ctx, sessionID)
		if err != nil || !ok {
			return domain.Session{}, false, err
		}
		if time.Now().UTC().After(session.ExpiresAt) {
			if err := m.CloseSession(ctx, sessionID); err != nil {
				slog.Warn("failed to close expired session", "session_id", sessionID, "error", err)
			}
			return domain.Session{}, false, nil
		}
		return session, true, nil
	}

	m.mu.RLock()
	containerID, ok := m.containers[sessionID]
	m.mu.RUnlock()
	if !ok {
		return domain.Session{}, false, nil
	}

	info, err := m.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return domain.Session{}, false, err
	}
	if info.ContainerJSONBase == nil || info.State == nil || !info.State.Running {
		return domain.Session{}, false, nil
	}

	return domain.Session{
		ID:            sessionID,
		WorkspacePath: m.cfg.Workdir,
		Runtime: domain.RuntimeRef{
			Kind:        domain.RuntimeKindDocker,
			ContainerID: containerID,
			Workdir:     m.cfg.Workdir,
		},
	}, true, nil
}

func (m *DockerManager) CloseSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	containerID, ok := m.containers[sessionID]
	if ok {
		delete(m.containers, sessionID)
	}
	m.mu.Unlock()

	if m.cfg.SessionStore != nil {
		if !ok {
			session, found, _ := m.cfg.SessionStore.Get(ctx, sessionID)
			if found && session.Runtime.ContainerID != "" {
				containerID = session.Runtime.ContainerID
				ok = true
			}
		}
		if delErr := m.cfg.SessionStore.Delete(ctx, sessionID); delErr != nil {
			slog.Warn("failed to delete session from store", "session_id", sessionID, "error", delErr)
		}
	}

	if !ok || containerID == "" {
		return nil
	}

	if stopErr := m.client.ContainerStop(ctx, containerID, container.StopOptions{}); stopErr != nil {
		slog.Warn("failed to stop container during session close", "container", containerID, "error", stopErr)
	}
	return m.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

func (m *DockerManager) resolveImage(metadata map[string]string) string {
	if m.cfg.ProfileKey != "" && len(m.cfg.ImageBundles) > 0 {
		if profile, ok := metadata[m.cfg.ProfileKey]; ok {
			if image, found := m.cfg.ImageBundles[profile]; found {
				return image
			}
		}
	}
	return m.cfg.DefaultImage
}

func (m *DockerManager) containerName(sessionID string) string {
	name := m.cfg.ContainerPrefix + "-" + sessionID
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '-'
	}, name)
	if len(name) > maxContainerNameLen {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

func (m *DockerManager) cloneRepoInContainer(ctx context.Context, containerID, repoURL, repoRef string) error {
	args := []string{"git", "clone", "--depth", "1"}
	if repoRef != "" {
		args = append(args, "--branch", repoRef)
	}
	args = append(args, repoURL, m.cfg.Workdir)

	execCfg := container.ExecOptions{
		Cmd:          args,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := m.client.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	attachResp, err := m.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()
	_, _ = io.Copy(io.Discard, attachResp.Reader)

	inspected, err := m.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("exec inspect: %w", err)
	}
	if inspected.ExitCode != 0 {
		return fmt.Errorf("git clone exited %d", inspected.ExitCode)
	}
	return nil
}
