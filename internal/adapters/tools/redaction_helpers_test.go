package tools

import (
	"strings"
	"testing"
)

func TestRedactSensitiveText(t *testing.T) {
	input := "url=https://svc/path?token=abc123 bearer abc.def.ghi redis://user:pass@host:6379"
	redacted := redactSensitiveText(input)

	expectedSnippets := []string{
		"token=[REDACTED]",
		"bearer [REDACTED]",
		"redis://user:[REDACTED]@host:6379",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(strings.ToLower(redacted), strings.ToLower(snippet)) {
			t.Fatalf("expected redacted text to contain %q, got %q", snippet, redacted)
		}
	}
}
