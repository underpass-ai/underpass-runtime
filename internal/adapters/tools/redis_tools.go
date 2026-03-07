package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	errRedisKeyRequired     = "key is required"
	errRedisKeyOutsideAllow = "key outside profile allowlist"
)

type RedisGetHandler struct {
	client redisClient
}

type RedisMGetHandler struct {
	client redisClient
}

type RedisScanHandler struct {
	client redisClient
}

type RedisTTLHandler struct {
	client redisClient
}

type RedisExistsHandler struct {
	client redisClient
}

type RedisSetHandler struct {
	client redisClient
}

type RedisDelHandler struct {
	client redisClient
}

type redisClient interface {
	Get(ctx context.Context, endpoint, key string) (string, error)
	MGet(ctx context.Context, endpoint string, keys []string) ([]any, error)
	Scan(ctx context.Context, endpoint string, cursor uint64, match string, count int64) ([]string, uint64, error)
	TTL(ctx context.Context, endpoint, key string) (time.Duration, error)
	Exists(ctx context.Context, endpoint string, keys []string) (int64, error)
	Set(ctx context.Context, endpoint, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, endpoint string, keys []string) (int64, error)
}

type liveRedisClient struct{}

func NewRedisGetHandler(client redisClient) *RedisGetHandler {
	return &RedisGetHandler{client: ensureRedisClient(client)}
}

func NewRedisMGetHandler(client redisClient) *RedisMGetHandler {
	return &RedisMGetHandler{client: ensureRedisClient(client)}
}

func NewRedisScanHandler(client redisClient) *RedisScanHandler {
	return &RedisScanHandler{client: ensureRedisClient(client)}
}

func NewRedisTTLHandler(client redisClient) *RedisTTLHandler {
	return &RedisTTLHandler{client: ensureRedisClient(client)}
}

func NewRedisExistsHandler(client redisClient) *RedisExistsHandler {
	return &RedisExistsHandler{client: ensureRedisClient(client)}
}

func NewRedisSetHandler(client redisClient) *RedisSetHandler {
	return &RedisSetHandler{client: ensureRedisClient(client)}
}

func NewRedisDelHandler(client redisClient) *RedisDelHandler {
	return &RedisDelHandler{client: ensureRedisClient(client)}
}

func (h *RedisGetHandler) Name() string {
	return "redis.get"
}

func (h *RedisGetHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
		Key       string `json:"key"`
		MaxBytes  int    `json:"max_bytes"`
		TimeoutMS int    `json:"timeout_ms"`
	}{
		MaxBytes:  65536,
		TimeoutMS: 2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.get args",
				Retryable: false,
			}
		}
	}

	key := strings.TrimSpace(request.Key)
	if key == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRedisKeyRequired,
			Retryable: false,
		}
	}
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 65536)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !keyAllowedByProfile(key, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRedisKeyOutsideAllow,
			Retryable: false,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	value, err := h.client.Get(runCtx, endpoint, key)
	if err != nil && err != redis.Nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis get failed: %v", err),
			Retryable: true,
		}
	}

	if err == redis.Nil {
		return app.ToolRunResult{
			Logs: []domain.LogLine{{
				At:      time.Now().UTC(),
				Channel: "stdout",
				Message: "redis get completed",
			}},
			Output: map[string]any{
				"profile_id": profile.ID,
				"key":        key,
				"found":      false,
			},
		}, nil
	}

	valueBytes := []byte(value)
	truncated := false
	if len(valueBytes) > maxBytes {
		valueBytes = valueBytes[:maxBytes]
		truncated = true
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis get completed",
		}},
		Output: map[string]any{
			"profile_id":   profile.ID,
			"key":          key,
			"found":        true,
			"value_base64": base64.StdEncoding.EncodeToString(valueBytes),
			"value_bytes":  len(valueBytes),
			"truncated":    truncated,
		},
	}, nil
}

func (h *RedisMGetHandler) Name() string {
	return "redis.mget"
}

func (h *RedisMGetHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string   `json:"profile_id"`
		Keys      []string `json:"keys"`
		MaxBytes  int      `json:"max_bytes"`
		TimeoutMS int      `json:"timeout_ms"`
	}{
		MaxBytes:  262144,
		TimeoutMS: 3000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.mget args",
				Retryable: false,
			}
		}
	}

	keys, validationErr := normalizeRedisKeys(request.Keys)
	if validationErr != nil {
		return app.ToolRunResult{}, validationErr
	}
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 262144)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 3000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	for _, key := range keys {
		if !keyAllowedByProfile(key, profile) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   errRedisKeyOutsideAllow,
				Retryable: false,
			}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	values, err := h.client.MGet(runCtx, endpoint, keys)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis mget failed: %v", err),
			Retryable: true,
		}
	}

	entries := make([]map[string]any, 0, len(keys))
	foundCount := 0
	totalBytes := 0
	truncated := false
	for idx, key := range keys {
		entry, found, bytesUsed, wasTruncated := processMGetEntry(idx, key, values, maxBytes-totalBytes)
		if found {
			foundCount++
		}
		totalBytes += bytesUsed
		if wasTruncated {
			truncated = true
		}
		entries = append(entries, entry)
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis mget completed",
		}},
		Output: map[string]any{
			"profile_id":  profile.ID,
			"entries":     entries,
			"key_count":   len(keys),
			"found_count": foundCount,
			"total_bytes": totalBytes,
			"truncated":   truncated,
		},
	}, nil
}

func (h *RedisScanHandler) Name() string {
	return "redis.scan"
}

func (h *RedisScanHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
		Prefix    string `json:"prefix"`
		Cursor    uint64 `json:"cursor"`
		MaxKeys   int    `json:"max_keys"`
		CountHint int64  `json:"count_hint"`
		TimeoutMS int    `json:"timeout_ms"`
	}{
		Cursor:    0,
		MaxKeys:   200,
		CountHint: 100,
		TimeoutMS: 3000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.scan args",
				Retryable: false,
			}
		}
	}

	prefix := strings.TrimSpace(request.Prefix)
	if prefix == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "prefix is required",
			Retryable: false,
		}
	}
	maxKeys := clampInt(request.MaxKeys, 1, 1000, 200)
	countHint := clampCountHint(request.CountHint)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 3000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !prefixAllowedByProfile(prefix, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "prefix outside profile allowlist",
			Retryable: false,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	keys, nextCursor, truncated, scanErr := redisScanKeys(runCtx, h.client, endpoint, request.Cursor, prefix, maxKeys, countHint)
	if scanErr != nil {
		return app.ToolRunResult{}, scanErr
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis scan completed",
		}},
		Output: map[string]any{
			"profile_id":  profile.ID,
			"prefix":      prefix,
			"keys":        keys,
			"count":       len(keys),
			"next_cursor": nextCursor,
			"truncated":   truncated,
		},
	}, nil
}

// clampCountHint normalises a SCAN COUNT hint to the range [1, 1000].
func clampCountHint(hint int64) int64 {
	if hint <= 0 {
		return 100
	}
	if hint > 1000 {
		return 1000
	}
	return hint
}

// redisScanKeys iterates SCAN pages until maxKeys is reached or the cursor
// wraps to zero, returning the collected keys, the final cursor value, and
// whether the result was truncated.
func redisScanKeys(ctx context.Context, client redisClient, endpoint string, initialCursor uint64, prefix string, maxKeys int, countHint int64) ([]string, uint64, bool, *domain.Error) {
	cursor := initialCursor
	keys := make([]string, 0, maxKeys)
	truncated := false
	match := prefix + "*"
	for len(keys) < maxKeys {
		batch, nextCursor, err := client.Scan(ctx, endpoint, cursor, match, countHint)
		if err != nil {
			return nil, 0, false, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   fmt.Sprintf("redis scan failed: %v", err),
				Retryable: true,
			}
		}
		var capped bool
		keys, capped = appendScanBatch(keys, batch, prefix, maxKeys)
		if capped {
			truncated = true
		}
		cursor = nextCursor
		if cursor == 0 || capped {
			break
		}
	}
	return keys, cursor, truncated, nil
}

func (h *RedisTTLHandler) Name() string {
	return "redis.ttl"
}

func (h *RedisTTLHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
		Key       string `json:"key"`
		TimeoutMS int    `json:"timeout_ms"`
	}{
		TimeoutMS: 2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.ttl args",
				Retryable: false,
			}
		}
	}

	key := strings.TrimSpace(request.Key)
	if key == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRedisKeyRequired,
			Retryable: false,
		}
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !keyAllowedByProfile(key, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRedisKeyOutsideAllow,
			Retryable: false,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	ttl, err := h.client.TTL(runCtx, endpoint, key)
	if err != nil && err != redis.Nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis ttl failed: %v", err),
			Retryable: true,
		}
	}

	status := "expiring"
	exists := true
	seconds := int64(ttl.Seconds())
	if ttl == -2*time.Second || err == redis.Nil {
		status = "missing"
		exists = false
		seconds = -2
	} else if ttl == -1*time.Second {
		status = "no_expiry"
		seconds = -1
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis ttl completed",
		}},
		Output: map[string]any{
			"profile_id":  profile.ID,
			"key":         key,
			"exists":      exists,
			"status":      status,
			"ttl_seconds": seconds,
		},
	}, nil
}

func (h *RedisExistsHandler) Name() string {
	return "redis.exists"
}

func (h *RedisExistsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string   `json:"profile_id"`
		Keys      []string `json:"keys"`
		TimeoutMS int      `json:"timeout_ms"`
	}{
		TimeoutMS: 2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.exists args",
				Retryable: false,
			}
		}
	}

	keys, validationErr := normalizeRedisKeys(request.Keys)
	if validationErr != nil {
		return app.ToolRunResult{}, validationErr
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	for _, key := range keys {
		if !keyAllowedByProfile(key, profile) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   errRedisKeyOutsideAllow,
				Retryable: false,
			}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	count, err := h.client.Exists(runCtx, endpoint, keys)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis exists failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis exists completed",
		}},
		Output: map[string]any{
			"profile_id":   profile.ID,
			"keys_checked": len(keys),
			"exists_count": count,
		},
	}, nil
}

func (h *RedisSetHandler) Name() string {
	return "redis.set"
}

func (h *RedisSetHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID     string  `json:"profile_id"`
		Key           string  `json:"key"`
		Value         *string `json:"value"`
		ValueEncoding string  `json:"value_encoding"`
		TTLSeconds    int     `json:"ttl_seconds"`
		TimeoutMS     int     `json:"timeout_ms"`
	}{
		ValueEncoding: "utf8",
		TimeoutMS:     2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.set args",
				Retryable: false,
			}
		}
	}

	key := strings.TrimSpace(request.Key)
	if key == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRedisKeyRequired,
			Retryable: false,
		}
	}
	if request.Value == nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "value is required",
			Retryable: false,
		}
	}
	if request.TTLSeconds <= 0 || request.TTLSeconds > 604800 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "ttl_seconds must be between 1 and 604800",
			Retryable: false,
		}
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	valueEncoding := strings.ToLower(strings.TrimSpace(request.ValueEncoding))
	if valueEncoding == "" {
		valueEncoding = "utf8"
	}
	var valueBytes []byte
	switch valueEncoding {
	case "utf8":
		valueBytes = []byte(*request.Value)
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(*request.Value)
		if err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "value is not valid base64",
				Retryable: false,
			}
		}
		valueBytes = decoded
	default:
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "value_encoding must be utf8 or base64",
			Retryable: false,
		}
	}

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if profile.ReadOnly {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "profile is read_only",
			Retryable: false,
		}
	}
	if !keyAllowedByProfile(key, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRedisKeyOutsideAllow,
			Retryable: false,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	if err := h.client.Set(runCtx, endpoint, key, valueBytes, time.Duration(request.TTLSeconds)*time.Second); err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis set failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis set completed",
		}},
		Output: map[string]any{
			"profile_id":   profile.ID,
			"key":          key,
			"ttl_seconds":  request.TTLSeconds,
			"value_bytes":  len(valueBytes),
			"value_format": valueEncoding,
			"written":      true,
		},
	}, nil
}

func (h *RedisDelHandler) Name() string {
	return "redis.del"
}

func (h *RedisDelHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string   `json:"profile_id"`
		Keys      []string `json:"keys"`
		TimeoutMS int      `json:"timeout_ms"`
	}{
		TimeoutMS: 2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid redis.del args",
				Retryable: false,
			}
		}
	}

	keys, validationErr := normalizeRedisKeys(request.Keys)
	if validationErr != nil {
		return app.ToolRunResult{}, validationErr
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRedisProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if profile.ReadOnly {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "profile is read_only",
			Retryable: false,
		}
	}
	for _, key := range keys {
		if !keyAllowedByProfile(key, profile) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   errRedisKeyOutsideAllow,
				Retryable: false,
			}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	deleted, err := h.client.Del(runCtx, endpoint, keys)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("redis del failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "redis del completed",
		}},
		Output: map[string]any{
			"profile_id":     profile.ID,
			"keys_requested": len(keys),
			"deleted":        deleted,
		},
	}, nil
}

func ensureRedisClient(client redisClient) redisClient {
	if client != nil {
		return client
	}
	return &liveRedisClient{}
}

func (c *liveRedisClient) Get(ctx context.Context, endpoint, key string) (string, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return client.Get(ctx, key).Result()
}

func (c *liveRedisClient) MGet(ctx context.Context, endpoint string, keys []string) ([]any, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.MGet(ctx, keys...).Result()
}

func (c *liveRedisClient) Scan(ctx context.Context, endpoint string, cursor uint64, match string, count int64) ([]string, uint64, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return nil, 0, err
	}
	defer client.Close()
	return client.Scan(ctx, cursor, match, count).Result()
}

func (c *liveRedisClient) TTL(ctx context.Context, endpoint, key string) (time.Duration, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	return client.TTL(ctx, key).Result()
}

func (c *liveRedisClient) Exists(ctx context.Context, endpoint string, keys []string) (int64, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	return client.Exists(ctx, keys...).Result()
}

func (c *liveRedisClient) Set(ctx context.Context, endpoint, key string, value []byte, ttl time.Duration) error {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Set(ctx, key, value, ttl).Err()
}

func (c *liveRedisClient) Del(ctx context.Context, endpoint string, keys []string) (int64, error) {
	client, err := openRedisClient(endpoint)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	return client.Del(ctx, keys...).Result()
}

func openRedisClient(endpoint string) (*redis.Client, error) {
	candidate := strings.TrimSpace(endpoint)
	if candidate == "" {
		return nil, fmt.Errorf("redis endpoint is empty")
	}

	var options *redis.Options
	var err error
	if strings.Contains(candidate, "://") {
		options, err = redis.ParseURL(candidate)
		if err != nil {
			return nil, err
		}
	} else {
		options = &redis.Options{Addr: candidate}
	}

	if strings.TrimSpace(options.Addr) == "" {
		return nil, fmt.Errorf("redis endpoint addr is empty")
	}

	return redis.NewClient(options), nil
}

func resolveRedisProfile(session domain.Session, requestedProfileID string) (connectionProfile, string, *domain.Error) {
	return resolveTypedProfile(session, requestedProfileID,
		[]string{"redis"}, "dev.redis",
		"localhost:6379")
}

func keyAllowedByProfile(key string, profile connectionProfile) bool {
	prefixes := extractProfileStringList(profile.Scopes, "key_prefixes")
	if len(prefixes) == 0 {
		return false
	}
	for _, prefix := range prefixes {
		if prefix == "*" || strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func prefixAllowedByProfile(prefix string, profile connectionProfile) bool {
	prefixes := extractProfileStringList(profile.Scopes, "key_prefixes")
	if len(prefixes) == 0 {
		return false
	}
	for _, allowed := range prefixes {
		if allowed == "*" || strings.HasPrefix(prefix, allowed) || strings.HasPrefix(allowed, prefix) {
			return true
		}
	}
	return false
}

func extractProfileStringList(scopes map[string]any, key string) []string {
	raw, found := scopes[key]
	if !found {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value := strings.TrimSpace(item)
			if value != "" {
				out = append(out, value)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			asString, ok := item.(string)
			if !ok {
				continue
			}
			value := strings.TrimSpace(asString)
			if value != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeRedisKeys(keys []string) ([]string, *domain.Error) {
	if len(keys) == 0 {
		return nil, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "keys is required",
			Retryable: false,
		}
	}
	if len(keys) > 200 {
		return nil, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "keys exceeds max size",
			Retryable: false,
		}
	}

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		candidate := strings.TrimSpace(key)
		if candidate == "" {
			return nil, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "keys must not contain empty entries",
				Retryable: false,
			}
		}
		out = append(out, candidate)
	}
	return out, nil
}

func redisValueToBytes(value any) []byte {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []byte(typed)
	case []byte:
		return typed
	default:
		return []byte(fmt.Sprint(typed))
	}
}

func processMGetEntry(idx int, key string, values []any, remaining int) (map[string]any, bool, int, bool) {
	entry := map[string]any{"key": key, "found": false}
	if idx >= len(values) || values[idx] == nil {
		return entry, false, 0, false
	}
	raw := redisValueToBytes(values[idx])
	if len(raw) == 0 {
		return entry, false, 0, false
	}
	valueTrimmed := false
	if remaining <= 0 {
		raw = []byte{}
		valueTrimmed = true
	} else if len(raw) > remaining {
		raw = raw[:remaining]
		valueTrimmed = true
	}
	entry["found"] = true
	entry["value_base64"] = base64.StdEncoding.EncodeToString(raw)
	entry["value_bytes"] = len(raw)
	entry["value_trimmed"] = valueTrimmed
	return entry, true, len(raw), valueTrimmed
}

func appendScanBatch(keys []string, batch []string, prefix string, maxKeys int) ([]string, bool) {
	for _, key := range batch {
		if len(keys) >= maxKeys {
			return keys, true
		}
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	if len(keys) >= maxKeys {
		return keys, true
	}
	return keys, false
}
