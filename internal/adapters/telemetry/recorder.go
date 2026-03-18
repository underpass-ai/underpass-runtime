package telemetry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// valkeyClient defines the minimal Valkey/Redis operations used by the recorder.
type valkeyClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	LTrim(ctx context.Context, key string, start, stop int64) *redis.StatusCmd
	LLen(ctx context.Context, key string) *redis.IntCmd
}

// ValkeyRecorder implements TelemetryRecorder backed by a Valkey list.
// Records are appended to a per-tool list for later aggregation.
type ValkeyRecorder struct {
	client    valkeyClient
	keyPrefix string
	ttl       time.Duration
}

// NewValkeyRecorder creates a recorder backed by a pre-configured Valkey client.
func NewValkeyRecorder(client valkeyClient, keyPrefix string, ttl time.Duration) *ValkeyRecorder {
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = "workspace:telemetry"
	}
	if !strings.HasSuffix(keyPrefix, ":") {
		keyPrefix += ":"
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour // 7 days default
	}
	return &ValkeyRecorder{client: client, keyPrefix: keyPrefix, ttl: ttl}
}

// NewValkeyRecorderFromAddress creates a ValkeyRecorder by connecting to a Valkey
// instance. Returns an error if the connection cannot be established.
func NewValkeyRecorderFromAddress(ctx context.Context, address, password string, db int, keyPrefix string, ttl time.Duration, tlsCfg *tls.Config) (*ValkeyRecorder, error) {
	client := redis.NewClient(&redis.Options{
		Addr:      strings.TrimSpace(address),
		Password:  password,
		DB:        db,
		TLSConfig: tlsCfg,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("telemetry valkey ping: %w", err)
	}
	return NewValkeyRecorder(client, keyPrefix, ttl), nil
}

// Record appends a telemetry record to the per-tool list.
func (r *ValkeyRecorder) Record(ctx context.Context, record app.TelemetryRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("telemetry marshal: %w", err)
	}
	key := r.toolKey(record.ToolName)
	if err := r.client.RPush(ctx, key, string(data)).Err(); err != nil {
		return fmt.Errorf("telemetry rpush: %w", err)
	}
	return nil
}

// ReadTool returns all telemetry records for a given tool. This is used by the
// aggregator to compute stats.
func (r *ValkeyRecorder) ReadTool(ctx context.Context, toolName string) ([]app.TelemetryRecord, error) {
	key := r.toolKey(toolName)
	items, err := r.client.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("telemetry lrange: %w", err)
	}
	records := make([]app.TelemetryRecord, 0, len(items))
	for i := range items {
		var rec app.TelemetryRecord
		if unmarshalErr := json.Unmarshal([]byte(items[i]), &rec); unmarshalErr != nil {
			continue // skip corrupted entries
		}
		records = append(records, rec)
	}
	return records, nil
}

// Len returns the number of records for a tool.
func (r *ValkeyRecorder) Len(ctx context.Context, toolName string) (int64, error) {
	return r.client.LLen(ctx, r.toolKey(toolName)).Result()
}

// Trim keeps only the last maxRecords entries for a tool.
func (r *ValkeyRecorder) Trim(ctx context.Context, toolName string, maxRecords int64) error {
	if maxRecords <= 0 {
		return nil
	}
	key := r.toolKey(toolName)
	return r.client.LTrim(ctx, key, -maxRecords, -1).Err()
}

func (r *ValkeyRecorder) toolKey(toolName string) string {
	return r.keyPrefix + strings.ReplaceAll(toolName, ".", ":")
}

// NoopRecorder is a telemetry recorder that discards all records.
type NoopRecorder struct{}

// Record discards the record. Always returns nil.
func (NoopRecorder) Record(context.Context, app.TelemetryRecord) error { return nil }
