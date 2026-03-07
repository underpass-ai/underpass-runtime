package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type LocalManager struct {
	basePath string

	mu      sync.RWMutex
	session map[string]domain.Session
	roots   map[string]string
}

func NewLocalManager(basePath string) *LocalManager {
	return &LocalManager{
		basePath: basePath,
		session:  map[string]domain.Session{},
		roots:    map[string]string{},
	}
}

func (m *LocalManager) CreateSession(ctx context.Context, req app.CreateSessionRequest) (domain.Session, error) {
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = newSessionID()
	}

	workspaceRoot := filepath.Join(m.basePath, req.Principal.TenantID, sessionID)
	repoRoot := filepath.Join(workspaceRoot, "repo")

	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		return domain.Session{}, fmt.Errorf("create workspace: %w", err)
	}

	if req.SourceRepoPath != "" {
		if err := copyDirectory(req.SourceRepoPath, repoRoot); err != nil {
			return domain.Session{}, fmt.Errorf("copy source repo: %w", err)
		}
	} else if req.RepoURL != "" {
		if err := cloneRepo(ctx, req.RepoURL, req.RepoRef, repoRoot); err != nil {
			return domain.Session{}, err
		}
	}

	allowedPaths := req.AllowedPaths
	if len(allowedPaths) == 0 {
		allowedPaths = []string{"."}
	}

	now := time.Now().UTC()
	session := domain.Session{
		ID:            sessionID,
		WorkspacePath: repoRoot,
		Runtime: domain.RuntimeRef{
			Kind:    domain.RuntimeKindLocal,
			Workdir: repoRoot,
		},
		RepoURL:      req.RepoURL,
		RepoRef:      req.RepoRef,
		AllowedPaths: allowedPaths,
		Principal:    req.Principal,
		Metadata:     req.Metadata,
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Duration(req.ExpiresInSecond) * time.Second),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.session[sessionID] = session
	m.roots[sessionID] = workspaceRoot
	return session, nil
}

func (m *LocalManager) GetSession(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	m.mu.RLock()
	session, ok := m.session[sessionID]
	m.mu.RUnlock()
	if !ok {
		return domain.Session{}, false, nil
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		_ = m.CloseSession(ctx, sessionID)
		return domain.Session{}, false, nil
	}
	return session, true, nil
}

func (m *LocalManager) CloseSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	root, ok := m.roots[sessionID]
	if ok {
		delete(m.roots, sessionID)
	}
	delete(m.session, sessionID)
	m.mu.Unlock()

	if !ok {
		return nil
	}
	if root == "" || root == "/" {
		return fmt.Errorf("refusing to remove invalid workspace root")
	}
	return os.RemoveAll(root)
}

func cloneRepo(ctx context.Context, repoURL, repoRef, targetPath string) error {
	args := []string{"clone", "--depth", "1"}
	if repoRef != "" {
		args = append(args, "--branch", repoRef)
	}
	args = append(args, repoURL, targetPath)

	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %w: %s", err, string(output))
	}
	return nil
}

func copyDirectory(src, dst string) error {
	sourceAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if err := filepath.Walk(sourceAbs, func(path string, info os.FileInfo, walkErr error) error {
		return copyDirectoryItem(sourceAbs, dst, path, info, walkErr)
	}); err != nil {
		return err
	}
	return nil
}

func copyDirectoryItem(sourceAbs, dst, path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	relPath, err := filepath.Rel(sourceAbs, path)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(dst, relPath)
	if info.IsDir() {
		return os.MkdirAll(targetPath, info.Mode())
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func newSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "session-fallback"
	}
	return "session-" + hex.EncodeToString(buf)
}
