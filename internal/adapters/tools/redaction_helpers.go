package tools

import (
	"regexp"
	"strings"
)

var (
	sensitiveTextQueryRe  = regexp.MustCompile(`(?i)(token|access_token|id_token|api_key|apikey|password|secret)=([^&\s]+)`)
	sensitiveTextBearerRe = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/\-]+`)
	sensitiveTextURLRe    = regexp.MustCompile(`://([^:/@\s]+):([^@/\s]+)@`)
)

func redactSensitiveText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	redacted := sensitiveTextQueryRe.ReplaceAllString(trimmed, "$1=[REDACTED]")
	redacted = sensitiveTextBearerRe.ReplaceAllString(redacted, "$1[REDACTED]")
	redacted = sensitiveTextURLRe.ReplaceAllString(redacted, "://$1:[REDACTED]@")
	return redacted
}
