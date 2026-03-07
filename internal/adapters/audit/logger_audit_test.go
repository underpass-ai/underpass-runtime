package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

func TestLoggerAuditRecord(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buffer, nil))
	a := NewLoggerAudit(logger)

	a.Record(context.Background(), app.AuditEvent{
		At:            time.Now().UTC(),
		SessionID:     "session-1",
		ToolName:      "fs.read",
		InvocationID:  "inv-1",
		CorrelationID: "corr-1",
		Status:        "succeeded",
		ActorID:       "alice",
		TenantID:      "tenant-a",
		Metadata:      map[string]string{"k": "v"},
	})

	if buffer.Len() == 0 {
		t.Fatal("expected log output")
	}

	var payload map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &payload); err != nil {
		t.Fatalf("unexpected json log format: %v", err)
	}
	if payload["msg"] != "audit.tool_invocation" {
		t.Fatalf("unexpected log message: %#v", payload)
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata in audit payload, got %#v", payload["metadata"])
	}
	if metadata["k"] != "v" {
		t.Fatalf("expected metadata to preserve non-sensitive values, got %#v", metadata["k"])
	}
}

func TestLoggerAuditRecord_RedactsSensitiveMetadata(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buffer, nil))
	a := NewLoggerAudit(logger)

	a.Record(context.Background(), app.AuditEvent{
		At:            time.Now().UTC(),
		SessionID:     "session-2",
		ToolName:      "redis.get",
		InvocationID:  "inv-2",
		CorrelationID: "corr-2",
		Status:        "succeeded",
		ActorID:       "bob",
		TenantID:      "tenant-b",
		Metadata: map[string]string{
			"connection_profile_endpoints_json": "{\"dev.redis\":\"redis://user:pass@host:6379\"}",
			"allowed_profiles":                  "dev.redis,dev.nats",
			"context":                           "https://svc/path?token=abc123",
		},
	})

	var payload map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &payload); err != nil {
		t.Fatalf("unexpected json log format: %v", err)
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata in audit payload, got %#v", payload["metadata"])
	}
	if metadata["connection_profile_endpoints_json"] != "[REDACTED]" {
		t.Fatalf("expected sensitive metadata key to be redacted, got %#v", metadata["connection_profile_endpoints_json"])
	}
	if metadata["allowed_profiles"] != "dev.redis,dev.nats" {
		t.Fatalf("expected non-sensitive metadata key to remain visible, got %#v", metadata["allowed_profiles"])
	}
	if metadata["context"] != "https://svc/path?token=[REDACTED]" {
		t.Fatalf("expected sensitive metadata value to be redacted, got %#v", metadata["context"])
	}
}
