package s3

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// fakeObjectClient records calls and returns configurable results.
type fakeObjectClient struct {
	putCalls     []putCall
	bucketExists bool
	bucketErr    error
	putErr       error
	makeBucketOK bool
}

type putCall struct {
	Bucket string
	Key    string
	Data   []byte
	Size   int64
}

func (f *fakeObjectClient) PutObject(_ context.Context, bucket, key string, reader io.Reader, size int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	if f.putErr != nil {
		return minio.UploadInfo{}, f.putErr
	}
	data, _ := io.ReadAll(reader)
	f.putCalls = append(f.putCalls, putCall{Bucket: bucket, Key: key, Data: data, Size: size})
	return minio.UploadInfo{}, nil
}

func (f *fakeObjectClient) BucketExists(_ context.Context, _ string) (bool, error) {
	return f.bucketExists, f.bucketErr
}

func (f *fakeObjectClient) MakeBucket(_ context.Context, _ string, _ minio.MakeBucketOptions) error {
	if f.makeBucketOK {
		return nil
	}
	return errors.New("make bucket failed")
}

func TestObjectKey(t *testing.T) {
	ts := time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)
	got := objectKey(ts)
	want := "audit/dt=2026-03-09/hour=14/snapshot-20260309T143000Z.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestObjectKeyMidnight(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := objectKey(ts)
	want := "audit/dt=2026-01-01/hour=00/snapshot-20260101T000000Z.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestSnapshotEnvelopeJSON(t *testing.T) {
	ts := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	envelope := snapshotEnvelope{
		Ts:    ts.Format(time.RFC3339),
		Count: 2,
		Policies: []domain.ToolPolicy{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Alpha: 91, Beta: 11},
			{ContextSignature: "gen:go:std", ToolID: "fs.read", Alpha: 50, Beta: 1},
		},
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got snapshotEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
	}
	if got.Ts != "2026-03-09T12:00:00Z" {
		t.Errorf("ts = %q, want 2026-03-09T12:00:00Z", got.Ts)
	}
	if len(got.Policies) != 2 {
		t.Fatalf("policies len = %d, want 2", len(got.Policies))
	}
	if got.Policies[0].Alpha != 91 {
		t.Errorf("policies[0].Alpha = %f, want 91", got.Policies[0].Alpha)
	}
}

func TestWriteSnapshot(t *testing.T) {
	fake := &fakeObjectClient{}
	store := NewAuditStore(fake, "policy-audit")

	ts := time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC)
	policies := []domain.ToolPolicy{
		{ContextSignature: "gen:go:std", ToolID: "fs.write", Alpha: 91, Beta: 11},
	}

	err := store.WriteSnapshot(context.Background(), ts, policies)
	if err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	if len(fake.putCalls) != 1 {
		t.Fatalf("expected 1 PutObject call, got %d", len(fake.putCalls))
	}

	call := fake.putCalls[0]
	if call.Bucket != "policy-audit" {
		t.Errorf("bucket = %q, want policy-audit", call.Bucket)
	}
	wantKey := "audit/dt=2026-03-09/hour=14/snapshot-20260309T140000Z.json"
	if call.Key != wantKey {
		t.Errorf("key = %q, want %q", call.Key, wantKey)
	}

	// Verify the written data is valid JSON with correct structure
	var envelope snapshotEnvelope
	if err := json.Unmarshal(call.Data, &envelope); err != nil {
		t.Fatalf("unmarshal written data: %v", err)
	}
	if envelope.Count != 1 {
		t.Errorf("envelope.Count = %d, want 1", envelope.Count)
	}
	if envelope.Policies[0].ToolID != "fs.write" {
		t.Errorf("tool_id = %q, want fs.write", envelope.Policies[0].ToolID)
	}
}

func TestWriteSnapshotPutError(t *testing.T) {
	fake := &fakeObjectClient{putErr: errors.New("s3 unavailable")}
	store := NewAuditStore(fake, "policy-audit")

	ts := time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC)
	err := store.WriteSnapshot(context.Background(), ts, []domain.ToolPolicy{
		{ContextSignature: "gen:go:std", ToolID: "fs.write"},
	})

	if err == nil {
		t.Fatal("expected error from PutObject failure")
	}
}

func TestEnsureBucketExists(t *testing.T) {
	fake := &fakeObjectClient{bucketExists: true}
	store := NewAuditStore(fake, "policy-audit")

	err := store.EnsureBucket(context.Background())
	if err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
}

func TestEnsureBucketCreates(t *testing.T) {
	fake := &fakeObjectClient{bucketExists: false, makeBucketOK: true}
	store := NewAuditStore(fake, "policy-audit")

	err := store.EnsureBucket(context.Background())
	if err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
}

func TestEnsureBucketCheckError(t *testing.T) {
	fake := &fakeObjectClient{bucketErr: errors.New("network error")}
	store := NewAuditStore(fake, "policy-audit")

	err := store.EnsureBucket(context.Background())
	if err == nil {
		t.Fatal("expected error from BucketExists failure")
	}
}

func TestNewAuditStoreFromConfig(t *testing.T) {
	store, err := NewAuditStoreFromConfig("localhost:9000", "access", "secret", "bucket", false)
	if err != nil {
		t.Fatalf("NewAuditStoreFromConfig: %v", err)
	}
	if store.bucket != "bucket" {
		t.Errorf("bucket = %q, want bucket", store.bucket)
	}
}
