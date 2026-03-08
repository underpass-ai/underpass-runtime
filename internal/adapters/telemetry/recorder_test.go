package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// --- Fake Valkey Client ---

type fakeValkeyClient struct {
	lists    map[string][]string
	pushErr  error
	rangeErr error
	lenErr   error
	trimErr  error
	pingErr  error
}

func newFakeValkeyClient() *fakeValkeyClient {
	return &fakeValkeyClient{lists: map[string][]string{}}
}

func (f *fakeValkeyClient) Ping(_ context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", f.pingErr)
}

func (f *fakeValkeyClient) RPush(_ context.Context, key string, values ...interface{}) *redis.IntCmd {
	if f.pushErr != nil {
		return redis.NewIntResult(0, f.pushErr)
	}
	for _, v := range values {
		f.lists[key] = append(f.lists[key], v.(string))
	}
	return redis.NewIntResult(int64(len(f.lists[key])), nil)
}

func (f *fakeValkeyClient) LRange(_ context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	if f.rangeErr != nil {
		return redis.NewStringSliceResult(nil, f.rangeErr)
	}
	items := f.lists[key]
	if start < 0 {
		start = 0
	}
	if stop < 0 || stop >= int64(len(items)) {
		stop = int64(len(items)) - 1
	}
	if start > stop || start >= int64(len(items)) {
		return redis.NewStringSliceResult([]string{}, nil)
	}
	return redis.NewStringSliceResult(items[start:stop+1], nil)
}

func (f *fakeValkeyClient) LTrim(_ context.Context, key string, start, stop int64) *redis.StatusCmd {
	if f.trimErr != nil {
		return redis.NewStatusResult("", f.trimErr)
	}
	items := f.lists[key]
	length := int64(len(items))
	if start < 0 {
		start = length + start
	}
	if stop < 0 {
		stop = length + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= length {
		stop = length - 1
	}
	if start > stop || start >= length {
		f.lists[key] = nil
		return redis.NewStatusResult("OK", nil)
	}
	f.lists[key] = items[start : stop+1]
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeValkeyClient) LLen(_ context.Context, key string) *redis.IntCmd {
	if f.lenErr != nil {
		return redis.NewIntResult(0, f.lenErr)
	}
	return redis.NewIntResult(int64(len(f.lists[key])), nil)
}

// --- Helper ---

func sampleRecord(tool, status string) app.TelemetryRecord {
	return app.TelemetryRecord{
		InvocationID:  "inv-001",
		SessionID:     "sess-001",
		ToolName:      tool,
		ToolFamily:    "fs",
		RuntimeKind:   "local",
		Status:        status,
		DurationMs:    42,
		OutputBytes:   1024,
		ArtifactCount: 1,
		ArtifactBytes: 2048,
		Timestamp:     time.Now().UTC(),
	}
}

// --- Tests ---

func TestValkeyRecorder_RecordAndRead(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	record := sampleRecord("fs.read", "succeeded")
	if err := rec.Record(context.Background(), record); err != nil {
		t.Fatalf("record error: %v", err)
	}

	records, err := rec.ReadTool(context.Background(), "fs.read")
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ToolName != "fs.read" {
		t.Fatalf("expected fs.read, got %s", records[0].ToolName)
	}
	if records[0].DurationMs != 42 {
		t.Fatalf("expected 42ms, got %d", records[0].DurationMs)
	}
}

func TestValkeyRecorder_MultipleRecords(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := rec.Record(ctx, sampleRecord("fs.read", "succeeded")); err != nil {
			t.Fatalf("record %d error: %v", i, err)
		}
	}

	n, err := rec.Len(ctx, "fs.read")
	if err != nil {
		t.Fatalf("len error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5, got %d", n)
	}
}

func TestValkeyRecorder_Trim(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if err := rec.Record(ctx, sampleRecord("fs.read", "succeeded")); err != nil {
			t.Fatalf("record error: %v", err)
		}
	}

	if err := rec.Trim(ctx, "fs.read", 3); err != nil {
		t.Fatalf("trim error: %v", err)
	}

	n, err := rec.Len(ctx, "fs.read")
	if err != nil {
		t.Fatalf("len error: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 after trim, got %d", n)
	}
}

func TestValkeyRecorder_TrimZeroNoOp(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	// Trim with maxRecords <= 0 should be a no-op
	if err := rec.Trim(context.Background(), "fs.read", 0); err != nil {
		t.Fatalf("trim zero error: %v", err)
	}
}

func TestValkeyRecorder_RecordPushError(t *testing.T) {
	client := newFakeValkeyClient()
	client.pushErr = errors.New("connection refused")
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	err := rec.Record(context.Background(), sampleRecord("fs.read", "succeeded"))
	if err == nil {
		t.Fatal("expected error on push failure")
	}
}

func TestValkeyRecorder_ReadRangeError(t *testing.T) {
	client := newFakeValkeyClient()
	client.rangeErr = errors.New("timeout")
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	_, err := rec.ReadTool(context.Background(), "fs.read")
	if err == nil {
		t.Fatal("expected error on range failure")
	}
}

func TestValkeyRecorder_ReadSkipsCorruptedEntries(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	ctx := context.Background()

	// Add a valid record
	if err := rec.Record(ctx, sampleRecord("fs.read", "succeeded")); err != nil {
		t.Fatalf("record error: %v", err)
	}
	// Add corrupted data directly
	key := rec.toolKey("fs.read")
	client.lists[key] = append(client.lists[key], "not valid json{{{")

	records, err := rec.ReadTool(ctx, "fs.read")
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(records))
	}
}

func TestValkeyRecorder_LenError(t *testing.T) {
	client := newFakeValkeyClient()
	client.lenErr = errors.New("timeout")
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	_, err := rec.Len(context.Background(), "fs.read")
	if err == nil {
		t.Fatal("expected error on len failure")
	}
}

func TestValkeyRecorder_TrimError(t *testing.T) {
	client := newFakeValkeyClient()
	client.trimErr = errors.New("timeout")
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	err := rec.Trim(context.Background(), "fs.read", 5)
	if err == nil {
		t.Fatal("expected error on trim failure")
	}
}

func TestValkeyRecorder_DefaultPrefix(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "", 24*time.Hour)

	if err := rec.Record(context.Background(), sampleRecord("fs.read", "succeeded")); err != nil {
		t.Fatalf("record error: %v", err)
	}
	// Default prefix is "workspace:telemetry:"
	expectedKey := "workspace:telemetry:fs:read"
	if len(client.lists[expectedKey]) != 1 {
		t.Fatalf("expected record at key %s, got keys: %v", expectedKey, keysOf(client.lists))
	}
}

func TestValkeyRecorder_DefaultTTL(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 0)

	if rec.ttl != 7*24*time.Hour {
		t.Fatalf("expected 7 day default TTL, got %v", rec.ttl)
	}
}

func TestValkeyRecorder_ToolKeyDotToColon(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)

	key := rec.toolKey("repo.test.run")
	expected := "ws:tel:repo:test:run"
	if key != expected {
		t.Fatalf("expected %s, got %s", expected, key)
	}
}

func TestValkeyRecorder_RecordPreservesAllFields(t *testing.T) {
	client := newFakeValkeyClient()
	rec := NewValkeyRecorder(client, "ws:tel", 24*time.Hour)
	ctx := context.Background()

	record := app.TelemetryRecord{
		InvocationID:  "inv-full",
		SessionID:     "sess-full",
		ToolName:      "repo.build",
		ToolFamily:    "repo",
		ToolsetID:     "core",
		RuntimeKind:   "docker",
		RepoLanguage:  "go",
		ProjectType:   "service",
		TenantID:      "tenant-1",
		Approved:      true,
		Status:        "succeeded",
		ErrorCode:     "",
		DurationMs:    500,
		OutputBytes:   4096,
		LogsBytes:     1024,
		ArtifactCount: 2,
		ArtifactBytes: 8192,
		Timestamp:     time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
	}

	if err := rec.Record(ctx, record); err != nil {
		t.Fatalf("record error: %v", err)
	}

	records, err := rec.ReadTool(ctx, "repo.build")
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1, got %d", len(records))
	}
	r := records[0]
	if r.InvocationID != "inv-full" {
		t.Fatalf("invocation_id: expected inv-full, got %s", r.InvocationID)
	}
	if r.RuntimeKind != "docker" {
		t.Fatalf("runtime_kind: expected docker, got %s", r.RuntimeKind)
	}
	if r.RepoLanguage != "go" {
		t.Fatalf("repo_language: expected go, got %s", r.RepoLanguage)
	}
	if r.ProjectType != "service" {
		t.Fatalf("project_type: expected service, got %s", r.ProjectType)
	}
	if !r.Approved {
		t.Fatal("approved: expected true")
	}
	if r.DurationMs != 500 {
		t.Fatalf("duration_ms: expected 500, got %d", r.DurationMs)
	}
	if r.ArtifactCount != 2 {
		t.Fatalf("artifact_count: expected 2, got %d", r.ArtifactCount)
	}
}

func TestNoopRecorder(t *testing.T) {
	rec := NoopRecorder{}
	if err := rec.Record(context.Background(), sampleRecord("fs.read", "succeeded")); err != nil {
		t.Fatalf("noop record should not fail: %v", err)
	}
}

func TestNewValkeyRecorderFromAddress_Unreachable(t *testing.T) {
	_, err := NewValkeyRecorderFromAddress(context.Background(), "localhost:1", "", 0, "ws:tel", time.Hour)
	if err == nil {
		t.Fatal("expected error for unreachable address")
	}
}

func TestValkeyRecorder_RecordRoundTrip(t *testing.T) {
	// Verify JSON round-trip preserves the record
	record := sampleRecord("fs.write", "failed")
	record.ErrorCode = "ERR_POLICY"

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded app.TelemetryRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.ErrorCode != "ERR_POLICY" {
		t.Fatalf("expected ERR_POLICY, got %s", decoded.ErrorCode)
	}
}

func keysOf(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
