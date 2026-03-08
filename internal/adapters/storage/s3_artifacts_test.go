package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// fakeS3Client is a hand-written fake that satisfies the s3Client interface.
type fakeS3Client struct {
	mu      sync.Mutex
	objects map[string]fakeS3Object
	putErr  error
	getErr  error
	listErr error
}

type fakeS3Object struct {
	data        []byte
	contentType string
	lastMod     time.Time
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{objects: make(map[string]fakeS3Object)}
}

func (f *fakeS3Client) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return nil, f.putErr
	}
	data, _ := io.ReadAll(params.Body)
	key := aws.ToString(params.Key)
	f.objects[key] = fakeS3Object{
		data:        data,
		contentType: aws.ToString(params.ContentType),
		lastMod:     time.Now().UTC(),
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Client) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	key := aws.ToString(params.Key)
	obj, ok := f.objects[key]
	if !ok {
		return nil, errors.New("NoSuchKey: The specified key does not exist")
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(obj.data)),
		ContentLength: aws.Int64(int64(len(obj.data))),
		ContentType:   aws.String(obj.contentType),
	}, nil
}

func (f *fakeS3Client) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	prefix := aws.ToString(params.Prefix)
	var contents []s3types.Object
	for key, obj := range f.objects {
		if strings.HasPrefix(key, prefix) {
			mod := obj.lastMod
			size := int64(len(obj.data))
			contents = append(contents, s3types.Object{
				Key:          aws.String(key),
				Size:         &size,
				LastModified: &mod,
			})
		}
	}
	return &s3.ListObjectsV2Output{
		Contents: contents,
		KeyCount: aws.Int32(int32(len(contents))),
	}, nil
}

func TestNewS3ArtifactStore_PrefixNormalization(t *testing.T) {
	fake := newFakeS3Client()

	store := NewS3ArtifactStore(fake, "bucket", "artifacts")
	if store.prefix != "artifacts/" {
		t.Fatalf("expected prefix artifacts/, got %s", store.prefix)
	}

	store = NewS3ArtifactStore(fake, "bucket", "artifacts/")
	if store.prefix != "artifacts/" {
		t.Fatalf("expected prefix artifacts/, got %s", store.prefix)
	}

	store = NewS3ArtifactStore(fake, "bucket", "")
	if store.prefix != "" {
		t.Fatalf("expected empty prefix, got %q", store.prefix)
	}

	store = NewS3ArtifactStore(fake, "bucket", "  ")
	if store.prefix != "" {
		t.Fatalf("expected empty prefix for whitespace, got %q", store.prefix)
	}
}

func TestS3ArtifactStore_SaveAndRead(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "test-bucket", "ws")

	payloads := []app.ArtifactPayload{
		{Name: "output.json", ContentType: "application/json", Data: []byte(`{"result":"ok"}`)},
		{Name: "logs.txt", ContentType: "text/plain", Data: []byte("line1\nline2\n")},
	}

	artifacts, err := store.Save(context.Background(), "inv-001", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	// Verify artifact metadata
	for i, art := range artifacts {
		if art.Name != payloads[i].Name {
			t.Fatalf("artifact %d: expected name %s, got %s", i, payloads[i].Name, art.Name)
		}
		if art.ContentType != payloads[i].ContentType {
			t.Fatalf("artifact %d: expected content type %s, got %s", i, payloads[i].ContentType, art.ContentType)
		}
		if art.SizeBytes != int64(len(payloads[i].Data)) {
			t.Fatalf("artifact %d: expected size %d, got %d", i, len(payloads[i].Data), art.SizeBytes)
		}
		if art.SHA256 == "" {
			t.Fatalf("artifact %d: expected non-empty SHA256", i)
		}
		if art.ID == "" {
			t.Fatalf("artifact %d: expected non-empty ID", i)
		}
		if !strings.HasPrefix(art.Path, "ws/inv-001/") {
			t.Fatalf("artifact %d: expected path prefix ws/inv-001/, got %s", i, art.Path)
		}
	}

	// Read back
	data, err := store.Read(context.Background(), artifacts[0].Path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(data) != `{"result":"ok"}` {
		t.Fatalf("read data mismatch: %s", string(data))
	}
}

func TestS3ArtifactStore_SaveEmptyPayloads(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "")

	artifacts, err := store.Save(context.Background(), "inv-empty", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if artifacts != nil {
		t.Fatalf("expected nil for empty payloads, got %v", artifacts)
	}
}

func TestS3ArtifactStore_SaveEmptyName(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "")

	payloads := []app.ArtifactPayload{
		{Name: "", Data: []byte("data")},
	}
	artifacts, err := store.Save(context.Background(), "inv-noname", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}
	if artifacts[0].Name != "artifact.bin" {
		t.Fatalf("expected fallback name artifact.bin, got %s", artifacts[0].Name)
	}
}

func TestS3ArtifactStore_SaveEmptyContentType(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "")

	payloads := []app.ArtifactPayload{
		{Name: "file.dat", ContentType: "", Data: []byte("data")},
	}
	artifacts, err := store.Save(context.Background(), "inv-noct", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}
	if artifacts[0].ContentType != "application/octet-stream" {
		t.Fatalf("expected default content type, got %s", artifacts[0].ContentType)
	}
}

func TestS3ArtifactStore_SavePutError(t *testing.T) {
	fake := newFakeS3Client()
	fake.putErr = errors.New("access denied")
	store := NewS3ArtifactStore(fake, "bucket", "")

	_, err := store.Save(context.Background(), "inv-err", []app.ArtifactPayload{
		{Name: "file.txt", Data: []byte("data")},
	})
	if err == nil {
		t.Fatal("expected put error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied error, got %v", err)
	}
}

func TestS3ArtifactStore_List(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "ws")

	// Save some artifacts
	payloads := []app.ArtifactPayload{
		{Name: "a.json", ContentType: "application/json", Data: []byte(`{"a":1}`)},
		{Name: "b.txt", ContentType: "text/plain", Data: []byte("hello")},
	}
	_, err := store.Save(context.Background(), "inv-list", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}

	// List
	artifacts, err := store.List(context.Background(), "inv-list")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	for _, art := range artifacts {
		if art.ID == "" {
			t.Fatal("expected non-empty artifact ID")
		}
		if art.SizeBytes <= 0 {
			t.Fatalf("expected positive size, got %d", art.SizeBytes)
		}
	}
}

func TestS3ArtifactStore_ListEmpty(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "ws")

	artifacts, err := store.List(context.Background(), "inv-missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected 0 artifacts, got %d", len(artifacts))
	}
}

func TestS3ArtifactStore_ListError(t *testing.T) {
	fake := newFakeS3Client()
	fake.listErr = errors.New("bucket not found")
	store := NewS3ArtifactStore(fake, "bucket", "ws")

	_, err := store.List(context.Background(), "inv-err")
	if err == nil {
		t.Fatal("expected list error")
	}
}

func TestS3ArtifactStore_ReadNotFound(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "")

	_, err := store.Read(context.Background(), "nonexistent/key")
	if err == nil {
		t.Fatal("expected error for missing object")
	}
}

func TestS3ArtifactStore_ReadGetError(t *testing.T) {
	fake := newFakeS3Client()
	fake.getErr = errors.New("internal error")
	store := NewS3ArtifactStore(fake, "bucket", "")

	_, err := store.Read(context.Background(), "some/key")
	if err == nil {
		t.Fatal("expected get error")
	}
}

func TestS3ArtifactStore_FullCycle(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "my-bucket", "artifacts")
	ctx := context.Background()

	// Save
	payloads := []app.ArtifactPayload{
		{Name: "coverage.out", ContentType: "text/plain", Data: []byte("mode: set\nok pkg 0.5s")},
	}
	saved, err := store.Save(ctx, "inv-full", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}

	// List
	listed, err := store.List(ctx, "inv-full")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(listed))
	}

	// Read
	data, err := store.Read(ctx, saved[0].Path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(data) != "mode: set\nok pkg 0.5s" {
		t.Fatalf("read content mismatch: %s", string(data))
	}

	// Verify SHA256 integrity
	if saved[0].SHA256 == "" {
		t.Fatal("expected non-empty SHA256")
	}
}

func TestS3ArtifactStore_NoPrefix(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "")

	payloads := []app.ArtifactPayload{
		{Name: "test.txt", Data: []byte("hello")},
	}
	saved, err := store.Save(context.Background(), "inv-np", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Path should start with invocationID directly (no prefix)
	if !strings.HasPrefix(saved[0].Path, "inv-np/") {
		t.Fatalf("expected path starting with inv-np/, got %s", saved[0].Path)
	}
}

func TestS3ArtifactStore_PathTraversalInName(t *testing.T) {
	fake := newFakeS3Client()
	store := NewS3ArtifactStore(fake, "bucket", "ws")

	payloads := []app.ArtifactPayload{
		{Name: "../../../etc/passwd", Data: []byte("data")},
	}
	saved, err := store.Save(context.Background(), "inv-traversal", payloads)
	if err != nil {
		t.Fatalf("save error: %v", err)
	}

	// sanitizeFilename should strip the path traversal
	if strings.Contains(saved[0].Name, "..") {
		t.Fatalf("filename should not contain .., got %s", saved[0].Name)
	}
	if strings.Contains(saved[0].Name, "/") {
		t.Fatalf("filename should not contain /, got %s", saved[0].Name)
	}
}
