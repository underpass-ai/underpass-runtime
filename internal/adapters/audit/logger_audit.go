package audit

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

var (
	auditSensitiveMetadataTokens = []string{
		"token",
		"secret",
		"password",
		"passwd",
		"api_key",
		"apikey",
		"authorization",
		"credential",
		"private_key",
		"connection_profile_endpoints_json",
	}
	auditSensitiveValueQueryRe  = regexp.MustCompile(`(?i)(token|access_token|id_token|api_key|apikey|password|secret)=([^&\s]+)`)
	auditSensitiveValueBearerRe = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/\-]+`)
	auditSensitiveValueURLRe    = regexp.MustCompile(`://([^:/@\s]+):([^@/\s]+)@`)
)

type LoggerAudit struct {
	logger *slog.Logger
}

func NewLoggerAudit(logger *slog.Logger) *LoggerAudit {
	return &LoggerAudit{logger: logger}
}

func (a *LoggerAudit) Record(_ context.Context, event app.AuditEvent) {
	metadata := redactAuditMetadata(event.Metadata)
	a.logger.Info("audit.tool_invocation",
		"at", event.At,
		"session_id", event.SessionID,
		"tool", event.ToolName,
		"invocation_id", event.InvocationID,
		"correlation_id", event.CorrelationID,
		"status", event.Status,
		"actor_id", event.ActorID,
		"tenant_id", event.TenantID,
		"metadata", metadata,
	)
}

func redactAuditMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return metadata
	}

	redacted := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if auditMetadataKeySensitive(key) {
			redacted[key] = "[REDACTED]"
			continue
		}
		redacted[key] = redactAuditValue(value)
	}
	return redacted
}

func auditMetadataKeySensitive(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range auditSensitiveMetadataTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func redactAuditValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	redacted := auditSensitiveValueQueryRe.ReplaceAllString(trimmed, "$1=[REDACTED]")
	redacted = auditSensitiveValueBearerRe.ReplaceAllString(redacted, "$1[REDACTED]")
	redacted = auditSensitiveValueURLRe.ReplaceAllString(redacted, "://$1:[REDACTED]@")
	return redacted
}
