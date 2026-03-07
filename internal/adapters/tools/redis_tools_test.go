package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeRedisClient struct {
	get    func(endpoint, key string) (string, error)
	mget   func(endpoint string, keys []string) ([]any, error)
	scan   func(endpoint string, cursor uint64, match string, count int64) ([]string, uint64, error)
	ttl    func(endpoint, key string) (time.Duration, error)
	exists func(endpoint string, keys []string) (int64, error)
	set    func(endpoint, key string, value []byte, ttl time.Duration) error
	del    func(endpoint string, keys []string) (int64, error)
}

func (f *fakeRedisClient) Get(_ context.Context, endpoint, key string) (string, error) {
	if f.get != nil {
		return f.get(endpoint, key)
	}
	return "", redis.Nil
}

func (f *fakeRedisClient) MGet(_ context.Context, endpoint string, keys []string) ([]any, error) {
	if f.mget != nil {
		return f.mget(endpoint, keys)
	}
	return []any{}, nil
}

func (f *fakeRedisClient) Scan(_ context.Context, endpoint string, cursor uint64, match string, count int64) ([]string, uint64, error) {
	if f.scan != nil {
		return f.scan(endpoint, cursor, match, count)
	}
	return []string{}, 0, nil
}

func (f *fakeRedisClient) TTL(_ context.Context, endpoint, key string) (time.Duration, error) {
	if f.ttl != nil {
		return f.ttl(endpoint, key)
	}
	return -2 * time.Second, nil
}

func (f *fakeRedisClient) Exists(_ context.Context, endpoint string, keys []string) (int64, error) {
	if f.exists != nil {
		return f.exists(endpoint, keys)
	}
	return 0, nil
}

func (f *fakeRedisClient) Set(_ context.Context, endpoint, key string, value []byte, ttl time.Duration) error {
	if f.set != nil {
		return f.set(endpoint, key, value, ttl)
	}
	return nil
}

func (f *fakeRedisClient) Del(_ context.Context, endpoint string, keys []string) (int64, error) {
	if f.del != nil {
		return f.del(endpoint, keys)
	}
	return 0, nil
}

func writableRedisSession() domain.Session {
	return domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"dev.redis","kind":"redis","read_only":false,"scopes":{"key_prefixes":["sandbox:"]}}]`,
		},
	}
}

func TestRedisGetHandler_Success(t *testing.T) {
	handler := NewRedisGetHandler(&fakeRedisClient{
		get: func(endpoint, key string) (string, error) {
			if endpoint == "" || key != "sandbox:todo:1" {
				t.Fatalf("unexpected get request: endpoint=%q key=%q", endpoint, key)
			}
			return "hello", nil
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:todo:1"}`))
	if err != nil {
		t.Fatalf("unexpected redis.get error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["found"] != true {
		t.Fatalf("expected found=true, got %#v", output["found"])
	}
}

func TestRedisGetHandler_DeniesKeyOutsideProfileScopes(t *testing.T) {
	handler := NewRedisGetHandler(&fakeRedisClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis","key":"prod:secret"}`))
	if err == nil {
		t.Fatal("expected key policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRedisScanHandler_Success(t *testing.T) {
	handler := NewRedisScanHandler(&fakeRedisClient{
		scan: func(endpoint string, cursor uint64, match string, count int64) ([]string, uint64, error) {
			if match != "sandbox:*" {
				t.Fatalf("unexpected scan match: %s", match)
			}
			return []string{"sandbox:todo:1", "sandbox:todo:2"}, 0, nil
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis","prefix":"sandbox:"}`))
	if err != nil {
		t.Fatalf("unexpected redis.scan error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["count"] != 2 {
		t.Fatalf("unexpected scan count: %#v", output["count"])
	}
}

func TestRedisExistsHandler_MapsExecutionErrors(t *testing.T) {
	handler := NewRedisExistsHandler(&fakeRedisClient{
		exists: func(endpoint string, keys []string) (int64, error) {
			return 0, errors.New("dial failed")
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis","keys":["sandbox:todo:1"]}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRedisSetHandler_Success(t *testing.T) {
	handler := NewRedisSetHandler(&fakeRedisClient{
		set: func(endpoint, key string, value []byte, ttl time.Duration) error {
			if endpoint == "" || key != "sandbox:todo:1" {
				t.Fatalf("unexpected set target: endpoint=%q key=%q", endpoint, key)
			}
			if string(value) != "hello" {
				t.Fatalf("unexpected set payload: %q", string(value))
			}
			if ttl != 60*time.Second {
				t.Fatalf("unexpected ttl: %s", ttl)
			}
			return nil
		},
	})
	session := writableRedisSession()

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:todo:1","value":"aGVsbG8=","value_encoding":"base64","ttl_seconds":60}`),
	)
	if err != nil {
		t.Fatalf("unexpected redis.set error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["written"] != true {
		t.Fatalf("expected written=true, got %#v", output["written"])
	}
}

func TestRedisSetHandler_RequiresTTL(t *testing.T) {
	handler := NewRedisSetHandler(&fakeRedisClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:todo:1","value":"hello","ttl_seconds":0}`),
	)
	if err == nil {
		t.Fatal("expected invalid ttl error")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRedisSetHandler_DeniesKeyOutsideProfileScopes(t *testing.T) {
	handler := NewRedisSetHandler(&fakeRedisClient{})
	session := writableRedisSession()

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","key":"prod:secret","value":"hello","ttl_seconds":60}`),
	)
	if err == nil {
		t.Fatal("expected key policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRedisSetHandler_DeniesReadOnlyProfile(t *testing.T) {
	handler := NewRedisSetHandler(&fakeRedisClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:todo:1","value":"hello","ttl_seconds":60}`),
	)
	if err == nil {
		t.Fatal("expected read_only policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if err.Message != "profile is read_only" {
		t.Fatalf("unexpected error message: %q", err.Message)
	}
}

func TestRedisDelHandler_Success(t *testing.T) {
	handler := NewRedisDelHandler(&fakeRedisClient{
		del: func(endpoint string, keys []string) (int64, error) {
			if endpoint == "" {
				t.Fatal("expected endpoint")
			}
			if len(keys) != 2 {
				t.Fatalf("unexpected key count: %d", len(keys))
			}
			return 2, nil
		},
	})
	session := writableRedisSession()

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","keys":["sandbox:todo:1","sandbox:todo:2"]}`),
	)
	if err != nil {
		t.Fatalf("unexpected redis.del error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["deleted"] != int64(2) {
		t.Fatalf("expected deleted=2, got %#v", output["deleted"])
	}
}

func TestRedisDelHandler_DeniesKeyOutsideProfileScopes(t *testing.T) {
	handler := NewRedisDelHandler(&fakeRedisClient{})
	session := writableRedisSession()

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","keys":["prod:secret"]}`),
	)
	if err == nil {
		t.Fatal("expected key policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRedisDelHandler_DeniesReadOnlyProfile(t *testing.T) {
	handler := NewRedisDelHandler(&fakeRedisClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"dev.redis","keys":["sandbox:todo:1"]}`),
	)
	if err == nil {
		t.Fatal("expected read_only policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if err.Message != "profile is read_only" {
		t.Fatalf("unexpected error message: %q", err.Message)
	}
}

func TestRedisHandlers_ConstructorsAndNames(t *testing.T) {
	if NewRedisGetHandler(nil).Name() != "redis.get" {
		t.Fatal("unexpected redis.get name")
	}
	if NewRedisMGetHandler(nil).Name() != "redis.mget" {
		t.Fatal("unexpected redis.mget name")
	}
	if NewRedisScanHandler(nil).Name() != "redis.scan" {
		t.Fatal("unexpected redis.scan name")
	}
	if NewRedisTTLHandler(nil).Name() != "redis.ttl" {
		t.Fatal("unexpected redis.ttl name")
	}
	if NewRedisExistsHandler(nil).Name() != "redis.exists" {
		t.Fatal("unexpected redis.exists name")
	}
	if NewRedisSetHandler(nil).Name() != "redis.set" {
		t.Fatal("unexpected redis.set name")
	}
	if NewRedisDelHandler(nil).Name() != "redis.del" {
		t.Fatal("unexpected redis.del name")
	}
}

func TestRedisMGetHandler_SuccessAndTruncation(t *testing.T) {
	handler := NewRedisMGetHandler(&fakeRedisClient{
		mget: func(endpoint string, keys []string) ([]any, error) {
			if endpoint == "" || len(keys) != 2 {
				t.Fatalf("unexpected mget call: endpoint=%q keys=%#v", endpoint, keys)
			}
			return []any{"hello", "world"}, nil
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis","keys":["sandbox:a","sandbox:b"],"max_bytes":7}`))
	if err != nil {
		t.Fatalf("unexpected redis.mget error: %#v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["found_count"] != 2 {
		t.Fatalf("unexpected found_count: %#v", output["found_count"])
	}
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", output["truncated"])
	}
}

func TestRedisTTLHandler_Statuses(t *testing.T) {
	baseSession := domain.Session{
		Metadata: map[string]string{},
	}

	noExpiry := NewRedisTTLHandler(&fakeRedisClient{
		ttl: func(endpoint, key string) (time.Duration, error) { return -1 * time.Second, nil },
	})
	result, err := noExpiry.Invoke(context.Background(), baseSession, json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:x"}`))
	if err != nil {
		t.Fatalf("unexpected redis.ttl no-expiry error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "no_expiry" {
		t.Fatalf("unexpected ttl status: %#v", output["status"])
	}

	missing := NewRedisTTLHandler(&fakeRedisClient{
		ttl: func(endpoint, key string) (time.Duration, error) { return -2 * time.Second, nil },
	})
	result, err = missing.Invoke(context.Background(), baseSession, json.RawMessage(`{"profile_id":"dev.redis","key":"sandbox:y"}`))
	if err != nil {
		t.Fatalf("unexpected redis.ttl missing error: %#v", err)
	}
	output = result.Output.(map[string]any)
	if output["status"] != "missing" || output["exists"] != false {
		t.Fatalf("unexpected missing ttl output: %#v", output)
	}
}

func TestLiveRedisClientMethods_EndpointValidation(t *testing.T) {
	client := &liveRedisClient{}
	ctx := context.Background()

	if _, err := client.Get(ctx, "", "k"); err == nil {
		t.Fatal("expected get endpoint validation error")
	}
	if _, err := client.MGet(ctx, "", []string{"k"}); err == nil {
		t.Fatal("expected mget endpoint validation error")
	}
	if _, _, err := client.Scan(ctx, "", 0, "sandbox:*", 10); err == nil {
		t.Fatal("expected scan endpoint validation error")
	}
	if _, err := client.TTL(ctx, "", "k"); err == nil {
		t.Fatal("expected ttl endpoint validation error")
	}
	if _, err := client.Exists(ctx, "", []string{"k"}); err == nil {
		t.Fatal("expected exists endpoint validation error")
	}
	if err := client.Set(ctx, "", "k", []byte("v"), time.Second); err == nil {
		t.Fatal("expected set endpoint validation error")
	}
	if _, err := client.Del(ctx, "", []string{"k"}); err == nil {
		t.Fatal("expected del endpoint validation error")
	}
}

func TestRedisHelpers_ProfileResolutionAndValueCoercion(t *testing.T) {
	_, _, err := resolveRedisProfile(domain.Session{}, "")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected profile_id validation error, got %#v", err)
	}

	sessionWrongKind := domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"x","kind":"mongo","read_only":true,"scopes":{"key_prefixes":["sandbox:"]}}]`,
		},
	}
	_, _, err = resolveRedisProfile(sessionWrongKind, "x")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected wrong-kind error, got %#v", err)
	}

	if _, err := openRedisClient(""); err == nil {
		t.Fatal("expected openRedisClient empty endpoint error")
	}
	if _, err := openRedisClient("://bad-url"); err == nil {
		t.Fatal("expected openRedisClient invalid URL error")
	}
	client, err2 := openRedisClient("localhost:6379")
	if err2 != nil {
		t.Fatalf("unexpected openRedisClient tcp endpoint error: %v", err2)
	}
	_ = client.Close()

	if v := redisValueToBytes(nil); v != nil {
		t.Fatalf("expected nil redis value, got %#v", v)
	}
	if string(redisValueToBytes("abc")) != "abc" {
		t.Fatal("unexpected redis string coercion")
	}
	if string(redisValueToBytes([]byte("def"))) != "def" {
		t.Fatal("unexpected redis []byte coercion")
	}
	if string(redisValueToBytes(123)) != "123" {
		t.Fatal("unexpected redis numeric coercion")
	}
}
