package tlsutil

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceLogHandler_InjectsTraceID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewTraceLogHandler(inner)
	logger := slog.New(handler)

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "test message")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if entry["trace_id"] != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("expected trace_id, got %v", entry["trace_id"])
	}
	if entry["span_id"] != "00f067aa0ba902b7" {
		t.Fatalf("expected span_id, got %v", entry["span_id"])
	}
}

func TestTraceLogHandler_NoTraceContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewTraceLogHandler(inner)
	logger := slog.New(handler)

	logger.Info("no trace")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := entry["trace_id"]; ok {
		t.Fatal("expected no trace_id when no span context")
	}
}
