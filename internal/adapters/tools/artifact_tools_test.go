package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestArtifactUploadHandler_UploadsFileAsArtifact(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "dist", "app.txt")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const content = "hello artifact\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	handler := NewArtifactUploadHandler(nil)
	session := domain.Session{
		WorkspacePath: workspace,
		AllowedPaths:  []string{"."},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"dist/app.txt","name":"release.txt","content_type":"text/plain","max_bytes":1024}`))
	if err != nil {
		t.Fatalf("unexpected upload error: %#v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["artifact_name"] != "release.txt" {
		t.Fatalf("unexpected artifact_name: %#v", output["artifact_name"])
	}
	if output["size_bytes"] != len(content) {
		t.Fatalf("unexpected size_bytes: %#v", output["size_bytes"])
	}
	if output["truncated"] != false {
		t.Fatalf("expected truncated=false, got %#v", output["truncated"])
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact payload, got %d", len(result.Artifacts))
	}
	if result.Artifacts[0].Name != "release.txt" {
		t.Fatalf("unexpected artifact payload name: %s", result.Artifacts[0].Name)
	}
	if string(result.Artifacts[0].Data) != content {
		t.Fatalf("unexpected artifact payload content: %q", string(result.Artifacts[0].Data))
	}
}

func TestArtifactUploadHandler_PathRequired(t *testing.T) {
	handler := NewArtifactUploadHandler(nil)
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		AllowedPaths:  []string{"."},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"name":"x.txt"}`))
	if err == nil {
		t.Fatal("expected invalid_argument error")
	}
	if err.Code != "invalid_argument" {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestArtifactDownloadHandler_Base64(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "bin", "payload.bin")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := []byte{0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	handler := NewArtifactDownloadHandler(nil)
	session := domain.Session{
		WorkspacePath: workspace,
		AllowedPaths:  []string{"."},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"bin/payload.bin","encoding":"base64","max_bytes":1024}`))
	if err != nil {
		t.Fatalf("unexpected download error: %#v", err)
	}

	output := result.Output.(map[string]any)
	encoded, _ := output["content_base64"].(string)
	decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if decodeErr != nil {
		t.Fatalf("decode base64: %v", decodeErr)
	}
	if string(decoded) != string(content) {
		t.Fatalf("unexpected decoded content: %v", decoded)
	}
}

func TestArtifactDownloadHandler_UTF8Truncated(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "notes.txt")
	longContent := strings.Repeat("a", 2048)
	if err := os.WriteFile(filePath, []byte(longContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	handler := NewArtifactDownloadHandler(nil)
	session := domain.Session{
		WorkspacePath: workspace,
		AllowedPaths:  []string{"."},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"notes.txt","encoding":"utf8","max_bytes":1024}`))
	if err != nil {
		t.Fatalf("unexpected download error: %#v", err)
	}

	output := result.Output.(map[string]any)
	content, _ := output["content"].(string)
	if len(content) != 1024 {
		t.Fatalf("expected content length 1024, got %d", len(content))
	}
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", output["truncated"])
	}
}

func TestArtifactListHandler_ListsPattern(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"dist/a.txt":       "a",
		"dist/b.log":       "b",
		"dist/sub/c.txt":   "c",
		"dist/sub/skip.md": "d",
	}
	for rel, content := range files {
		full := filepath.Join(workspace, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	handler := NewArtifactListHandler(nil)
	session := domain.Session{
		WorkspacePath: workspace,
		AllowedPaths:  []string{"."},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"dist","recursive":true,"pattern":"*.txt","max_entries":10}`))
	if err != nil {
		t.Fatalf("unexpected list error: %#v", err)
	}

	output := result.Output.(map[string]any)
	entries, ok := output["artifacts"].([]artifactListEntry)
	if !ok {
		t.Fatalf("unexpected artifacts output type: %T", output["artifacts"])
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	slices.Sort(paths)
	expected := []string{"dist/a.txt", "dist/sub/c.txt"}
	if !slices.Equal(paths, expected) {
		t.Fatalf("unexpected listed paths: %#v", paths)
	}
}

func TestArtifactDownloadHandler_KubernetesRequiresRunner(t *testing.T) {
	handler := NewArtifactDownloadHandler(nil)
	session := domain.Session{
		WorkspacePath: "/workspace/repo",
		AllowedPaths:  []string{"."},
		Runtime: domain.RuntimeRef{
			Kind: domain.RuntimeKindKubernetes,
		},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"a.txt","encoding":"base64"}`))
	if err == nil {
		t.Fatal("expected execution_failed without runner")
	}
	if err.Code != "execution_failed" {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestArtifactHandlers_Names(t *testing.T) {
	if NewArtifactUploadHandler(nil).Name() != "artifact.upload" {
		t.Fatal("unexpected artifact.upload name")
	}
	if NewArtifactDownloadHandler(nil).Name() != "artifact.download" {
		t.Fatal("unexpected artifact.download name")
	}
	if NewArtifactListHandler(nil).Name() != "artifact.list" {
		t.Fatal("unexpected artifact.list name")
	}
}

func TestArtifactListHandler_KubernetesRemoteListing(t *testing.T) {
	runner := &fakeArtifactRunner{
		run: func(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "sh" {
				return app.CommandResult{}, fmt.Errorf("unexpected command: %s", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output: "/workspace/repo/dist/a.txt\n" +
					"/workspace/repo/dist/b.log\n",
			}, nil
		},
	}
	handler := NewArtifactListHandler(runner)
	session := domain.Session{
		WorkspacePath: "/workspace/repo",
		AllowedPaths:  []string{"."},
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"path":"dist","recursive":true,"pattern":"*.txt","max_entries":5}`))
	if err != nil {
		t.Fatalf("unexpected remote list error: %#v", err)
	}
	output := result.Output.(map[string]any)
	entries := output["artifacts"].([]artifactListEntry)
	if len(entries) != 1 || entries[0].Path != "dist/a.txt" {
		t.Fatalf("unexpected remote list entries: %#v", entries)
	}
}

func TestArtifactHelpers_MinInt(t *testing.T) {
	if artifactMinInt(1, 2) != 1 {
		t.Fatal("unexpected artifactMinInt for 1,2")
	}
	if artifactMinInt(3, 2) != 2 {
		t.Fatal("unexpected artifactMinInt for 3,2")
	}
}

type fakeArtifactRunner struct {
	run func(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeArtifactRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if f.run == nil {
		return app.CommandResult{}, fmt.Errorf("fake artifact runner not configured")
	}
	return f.run(ctx, session, spec)
}

func TestCollectFlatArtifactEntries_TwoFiles(t *testing.T) {
	workspace := t.TempDir()

	// Create two files in the workspace root
	files := []string{"alpha.txt", "beta.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("content"), 0o644); err != nil {
			t.Fatalf("write file %s: %v", name, err)
		}
	}

	entries, err := collectFlatArtifactEntries(workspace, workspace, "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	slices.Sort(paths)
	expected := []string{"alpha.txt", "beta.txt"}
	if !slices.Equal(paths, expected) {
		t.Fatalf("expected paths %v, got %v", expected, paths)
	}
}

func TestCollectFlatArtifactEntries_MaxEntriesLimit(t *testing.T) {
	workspace := t.TempDir()

	// Create three files
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	entries, err := collectFlatArtifactEntries(workspace, workspace, "", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) > 1 {
		t.Fatalf("expected at most 1 entry, got %d", len(entries))
	}
}

func TestCollectFlatArtifactEntries_NonExistentDirectory(t *testing.T) {
	workspace := t.TempDir()
	nonExistent := filepath.Join(workspace, "does-not-exist")

	_, err := collectFlatArtifactEntries(workspace, nonExistent, "", 10)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
	if err.Code != "execution_failed" {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}
