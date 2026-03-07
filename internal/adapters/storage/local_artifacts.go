package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type LocalArtifactStore struct {
	basePath string
}

func NewLocalArtifactStore(basePath string) *LocalArtifactStore {
	return &LocalArtifactStore{basePath: basePath}
}

func (s *LocalArtifactStore) Save(_ context.Context, invocationID string, payloads []app.ArtifactPayload) ([]domain.Artifact, error) {
	if len(payloads) == 0 {
		return nil, nil
	}

	invocationDir := filepath.Join(s.basePath, invocationID)
	if err := os.MkdirAll(invocationDir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}

	artifacts := make([]domain.Artifact, 0, len(payloads))
	for _, payload := range payloads {
		artifactID := newArtifactID()
		name := sanitizeFilename(payload.Name)
		if name == "" {
			name = "artifact.bin"
		}

		storedPath := filepath.Join(invocationDir, artifactID+"-"+name)
		if err := os.WriteFile(storedPath, payload.Data, 0o644); err != nil {
			return nil, fmt.Errorf("write artifact: %w", err)
		}

		hash := sha256.Sum256(payload.Data)
		artifacts = append(artifacts, domain.Artifact{
			ID:          artifactID,
			Name:        name,
			Path:        storedPath,
			ContentType: payload.ContentType,
			SizeBytes:   int64(len(payload.Data)),
			SHA256:      hex.EncodeToString(hash[:]),
			CreatedAt:   time.Now().UTC(),
		})
	}

	return artifacts, nil
}

func (s *LocalArtifactStore) List(_ context.Context, invocationID string) ([]domain.Artifact, error) {
	invocationDir := filepath.Join(s.basePath, invocationID)
	entries, err := os.ReadDir(invocationDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	artifacts := make([]domain.Artifact, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filePath := filepath.Join(invocationDir, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}

		hash, hashErr := fileSHA256(filePath)
		if hashErr != nil {
			return nil, hashErr
		}

		parts := strings.SplitN(entry.Name(), "-", 2)
		artifactID := parts[0]
		name := entry.Name()
		if len(parts) == 2 {
			name = parts[1]
		}

		artifacts = append(artifacts, domain.Artifact{
			ID:          artifactID,
			Name:        name,
			Path:        filePath,
			ContentType: "application/octet-stream",
			SizeBytes:   info.Size(),
			SHA256:      hash,
			CreatedAt:   info.ModTime().UTC(),
		})
	}

	return artifacts, nil
}

func (s *LocalArtifactStore) Read(_ context.Context, path string) ([]byte, error) {
	cleanBase := filepath.Clean(s.basePath)
	cleanPath := filepath.Clean(path)
	if cleanPath != cleanBase && !strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator)) {
		return nil, fmt.Errorf("artifact path outside base path")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	return data, nil
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

func newArtifactID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "artifact-fallback"
	}
	return "artifact-" + hex.EncodeToString(buf)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
