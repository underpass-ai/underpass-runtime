package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// s3Client defines the minimal S3 operations used by S3ArtifactStore.
type s3Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3ArtifactStore implements app.ArtifactStore backed by S3-compatible storage.
type S3ArtifactStore struct {
	client s3Client
	bucket string
	prefix string
}

// S3Config holds configuration for the S3 artifact store.
type S3Config struct {
	Bucket    string
	Prefix    string
	Endpoint  string // MinIO or custom S3-compatible endpoint
	Region    string
	AccessKey string
	SecretKey string
	PathStyle bool        // Required for MinIO
	UseSSL    bool        // Use HTTPS for S3 connections
	TLSConfig *tls.Config // Custom TLS config (e.g. custom CA)
}

// NewS3ArtifactStore creates an S3ArtifactStore from a pre-configured client.
func NewS3ArtifactStore(client s3Client, bucket, prefix string) *S3ArtifactStore {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &S3ArtifactStore{client: client, bucket: bucket, prefix: prefix}
}

// NewS3ArtifactStoreFromConfig creates an S3ArtifactStore using explicit config.
func NewS3ArtifactStoreFromConfig(ctx context.Context, cfg S3Config) (*S3ArtifactStore, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}

	if cfg.TLSConfig != nil {
		opts = append(opts, config.WithHTTPClient(&http.Client{
			Transport: &http.Transport{TLSClientConfig: cfg.TLSConfig},
		}))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 load config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = cfg.PathStyle
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)
	return NewS3ArtifactStore(client, cfg.Bucket, cfg.Prefix), nil
}

// Save persists artifact payloads to S3. Objects are stored at
// {prefix}{invocationID}/{artifactID}-{name}.
func (s *S3ArtifactStore) Save(ctx context.Context, invocationID string, payloads []app.ArtifactPayload) ([]domain.Artifact, error) {
	if len(payloads) == 0 {
		return nil, nil
	}

	artifacts := make([]domain.Artifact, 0, len(payloads))
	for i := range payloads {
		artifactID := newArtifactID()
		name := sanitizeFilename(payloads[i].Name)
		if name == "" {
			name = "artifact.bin"
		}

		key := s.prefix + invocationID + "/" + artifactID + "-" + name
		hash := sha256.Sum256(payloads[i].Data)
		hashHex := hex.EncodeToString(hash[:])

		contentType := payloads[i].ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		input := &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(payloads[i].Data),
			ContentType: aws.String(contentType),
		}

		if _, err := s.client.PutObject(ctx, input); err != nil {
			return nil, fmt.Errorf("s3 put artifact %s: %w", key, err)
		}

		artifacts = append(artifacts, domain.Artifact{
			ID:          artifactID,
			Name:        name,
			Path:        key,
			ContentType: contentType,
			SizeBytes:   int64(len(payloads[i].Data)),
			SHA256:      hashHex,
			CreatedAt:   time.Now().UTC(),
		})
	}

	return artifacts, nil
}

// List returns all artifacts for an invocation by listing S3 objects under
// {prefix}{invocationID}/.
func (s *S3ArtifactStore) List(ctx context.Context, invocationID string) ([]domain.Artifact, error) {
	listPrefix := s.prefix + invocationID + "/"
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(listPrefix),
	}

	output, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("s3 list artifacts: %w", err)
	}

	artifacts := make([]domain.Artifact, 0, len(output.Contents))
	for _, obj := range output.Contents {
		key := aws.ToString(obj.Key)
		filename := strings.TrimPrefix(key, listPrefix)
		if filename == "" {
			continue
		}

		parts := strings.SplitN(filename, "-", 2)
		artifactID := parts[0]
		name := filename
		if len(parts) == 2 {
			name = parts[1]
		}

		artifacts = append(artifacts, domain.Artifact{
			ID:          artifactID,
			Name:        name,
			Path:        key,
			ContentType: "application/octet-stream",
			SizeBytes:   aws.ToInt64(obj.Size),
			CreatedAt:   aws.ToTime(obj.LastModified),
		})
	}

	return artifacts, nil
}

// Read fetches an artifact's content from S3 by its key path.
func (s *S3ArtifactStore) Read(ctx context.Context, path string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	}

	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("s3 get artifact %s: %w", path, err)
	}
	defer func() { _ = output.Body.Close() }()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 read artifact body %s: %w", path, err)
	}
	return data, nil
}
