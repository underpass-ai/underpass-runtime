package tools

import (
	"path/filepath"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestResolvePath_AllowsWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"src"}}

	resolved, err := resolvePath(session, "src/main.go")
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if resolved != filepath.Join(root, "src", "main.go") {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}
}

func TestResolvePath_DeniesTraversalAndAllowlist(t *testing.T) {
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"src"}}

	if _, err := resolvePath(session, "../secrets.txt"); err == nil {
		t.Fatal("expected traversal to be denied")
	}
	if _, err := resolvePath(session, "docs/readme.md"); err == nil {
		t.Fatal("expected out-of-allowlist path to be denied")
	}
}

func TestPathAllowed(t *testing.T) {
	if !pathAllowed("a/b", []string{"."}) {
		t.Fatal("expected default allowlist to allow non-traversal path")
	}
	if pathAllowed("../a", []string{"."}) {
		t.Fatal("expected traversal to be denied")
	}
	if !pathAllowed("src/pkg", []string{"src"}) {
		t.Fatal("expected prefix allowlist match")
	}
}
