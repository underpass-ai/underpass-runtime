package storage

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// SnapshotStore creates and restores compressed workspace snapshots using
// an ArtifactStore as the backing storage. Snapshots are tar.gz archives.
type SnapshotStore struct {
	artifacts app.ArtifactStore
}

// NewSnapshotStore creates a SnapshotStore backed by the given ArtifactStore.
func NewSnapshotStore(artifacts app.ArtifactStore) *SnapshotStore {
	return &SnapshotStore{artifacts: artifacts}
}

// Create archives the workspace directory as a tar.gz and stores it as an
// artifact under the session ID.
func (s *SnapshotStore) Create(ctx context.Context, sessionID, workspaceDir string) (app.SnapshotRef, error) {
	data, err := createTarGz(workspaceDir)
	if err != nil {
		return app.SnapshotRef{}, fmt.Errorf("snapshot create tar: %w", err)
	}

	hash := sha256.Sum256(data)
	payloads := []app.ArtifactPayload{{
		Name:        "workspace-snapshot.tar.gz",
		ContentType: "application/gzip",
		Data:        data,
	}}

	// Store under a snapshot-specific invocation namespace to avoid collision.
	snapshotKey := "snapshot-" + sessionID
	artifacts, err := s.artifacts.Save(ctx, snapshotKey, payloads)
	if err != nil {
		return app.SnapshotRef{}, fmt.Errorf("snapshot save: %w", err)
	}
	if len(artifacts) == 0 {
		return app.SnapshotRef{}, fmt.Errorf("snapshot save returned no artifacts")
	}

	return app.SnapshotRef{
		ID:        artifacts[0].ID,
		SessionID: sessionID,
		Path:      artifacts[0].Path,
		Size:      int64(len(data)),
		CreatedAt: time.Now().UTC(),
		Checksum:  hex.EncodeToString(hash[:]),
	}, nil
}

// Restore extracts a snapshot archive into the target directory.
func (s *SnapshotStore) Restore(ctx context.Context, ref app.SnapshotRef, targetDir string) error {
	data, err := s.artifacts.Read(ctx, ref.Path)
	if err != nil {
		return fmt.Errorf("snapshot read: %w", err)
	}
	if err := extractTarGz(data, targetDir); err != nil {
		return fmt.Errorf("snapshot extract: %w", err)
	}
	return nil
}

// createTarGz compresses a directory into an in-memory tar.gz archive.
func createTarGz(srcDir string) ([]byte, error) {
	srcDir = filepath.Clean(srcDir)
	info, err := os.Stat(srcDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", srcDir)
	}

	var buf []byte
	pr, pw := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
		gw := gzip.NewWriter(pw)
		tw := tar.NewWriter(gw)
		walkErr := filepath.Walk(srcDir, func(path string, fi os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(srcDir, path)
			if relErr != nil {
				return relErr
			}
			if rel == "." {
				return nil
			}

			header, headerErr := tar.FileInfoHeader(fi, "")
			if headerErr != nil {
				return headerErr
			}
			header.Name = rel

			if writeErr := tw.WriteHeader(header); writeErr != nil {
				return writeErr
			}

			if fi.IsDir() {
				return nil
			}

			f, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			_, copyErr := io.Copy(tw, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		})
		_ = tw.Close()
		_ = gw.Close()
		_ = pw.CloseWithError(walkErr)
		errCh <- walkErr
	}()

	buf, readErr := io.ReadAll(pr)
	walkErr := <-errCh
	if walkErr != nil {
		return nil, walkErr
	}
	if readErr != nil {
		return nil, readErr
	}
	return buf, nil
}

// extractTarGz decompresses a tar.gz archive into the target directory.
func extractTarGz(data []byte, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}

	gr, err := gzip.NewReader(io.NopCloser(strings.NewReader(string(data))))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		header, headerErr := tr.Next()
		if headerErr == io.EOF {
			break
		}
		if headerErr != nil {
			return headerErr
		}

		target := filepath.Join(targetDir, filepath.Clean(header.Name))
		// Security: prevent path traversal
		if !strings.HasPrefix(target, filepath.Clean(targetDir)+string(filepath.Separator)) {
			return fmt.Errorf("invalid tar path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return mkErr
			}
		case tar.TypeReg:
			if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
				return mkErr
			}
			f, createErr := os.Create(target)
			if createErr != nil {
				return createErr
			}
			if _, copyErr := io.Copy(f, tr); copyErr != nil {
				_ = f.Close()
				return copyErr
			}
			if closeErr := f.Close(); closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}
