package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCapabilityFamilyName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "misc"},
		{name: "single", in: "health", want: "health"},
		{name: "dotted", in: "fs.read", want: "fs.*"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := capabilityFamilyName(tc.in)
			if got != tc.want {
				t.Fatalf("capabilityFamilyName(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultCapabilitiesMarkdownSynced(t *testing.T) {
	generated := normalizeNewlines(DefaultCapabilitiesMarkdown())

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	docPath := filepath.Join(moduleRoot, "docs", "CAPABILITY_CATALOG.md")

	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	checkedIn := normalizeNewlines(string(raw))
	if checkedIn != generated {
		t.Fatalf("capability catalog markdown is out of date; run `make catalog-docs`")
	}
}

func normalizeNewlines(raw string) string {
	return strings.ReplaceAll(raw, "\r\n", "\n")
}
