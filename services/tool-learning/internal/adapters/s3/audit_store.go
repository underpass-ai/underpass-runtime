package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// AuditStore implements app.PolicyAuditStore writing JSON snapshots to S3/MinIO.
type AuditStore struct {
	client *minio.Client
	bucket string
}

// NewAuditStore creates an audit store from an existing MinIO client.
func NewAuditStore(client *minio.Client, bucket string) *AuditStore {
	return &AuditStore{client: client, bucket: bucket}
}

// NewAuditStoreFromConfig creates an audit store connecting to MinIO/S3.
func NewAuditStoreFromConfig(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*AuditStore, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &AuditStore{client: client, bucket: bucket}, nil
}

// snapshotEnvelope wraps policies with metadata.
type snapshotEnvelope struct {
	Ts       string              `json:"ts"`
	Count    int                 `json:"count"`
	Policies []domain.ToolPolicy `json:"policies"`
}

// objectKey returns the S3 object key for a snapshot at the given timestamp.
func objectKey(ts time.Time) string {
	return fmt.Sprintf(
		"audit/dt=%s/hour=%02d/snapshot-%s.json",
		ts.Format("2006-01-02"),
		ts.Hour(),
		ts.Format("20060102T150405Z"),
	)
}

// WriteSnapshot persists a timestamped batch of policies as a JSON object in S3.
func (s *AuditStore) WriteSnapshot(ctx context.Context, ts time.Time, policies []domain.ToolPolicy) error {
	envelope := snapshotEnvelope{
		Ts:       ts.Format(time.RFC3339),
		Count:    len(policies),
		Policies: policies,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	key := objectKey(ts)
	reader := bytes.NewReader(data)

	_, err = s.client.PutObject(ctx, s.bucket, key, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	if err != nil {
		return fmt.Errorf("put audit snapshot: %w", err)
	}

	return nil
}

// EnsureBucket creates the audit bucket if it does not exist.
func (s *AuditStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if exists {
		return nil
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}
