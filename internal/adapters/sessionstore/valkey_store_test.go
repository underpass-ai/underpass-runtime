package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeValkeyClient struct {
	data    map[string]string
	pingErr error
	setErr  error
	getErr  error
	delErr  error
}

func (f *fakeValkeyClient) Ping(_ context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", f.pingErr)
}

func (f *fakeValkeyClient) Set(_ context.Context, key string, value interface{}, _ time.Duration) *redis.StatusCmd {
	if f.setErr != nil {
		return redis.NewStatusResult("", f.setErr)
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	switch typed := value.(type) {
	case []byte:
		f.data[key] = string(typed)
	case string:
		f.data[key] = typed
	default:
		payload, _ := json.Marshal(typed)
		f.data[key] = string(payload)
	}
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeValkeyClient) Get(_ context.Context, key string) *redis.StringCmd {
	if f.getErr != nil {
		return redis.NewStringResult("", f.getErr)
	}
	value, ok := f.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func (f *fakeValkeyClient) Del(_ context.Context, keys ...string) *redis.IntCmd {
	if f.delErr != nil {
		return redis.NewIntResult(0, f.delErr)
	}
	for _, key := range keys {
		delete(f.data, key)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func TestValkeyStore_SaveGetDelete(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{data: map[string]string{}}, "workspace:test:session", time.Hour)

	session := domain.Session{
		ID:            "session-1",
		WorkspacePath: "/workspace/repo",
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(5 * time.Minute),
	}
	if err := store.Save(context.Background(), session); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, found, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatal("expected session to be found")
	}
	if loaded.ID != session.ID {
		t.Fatalf("unexpected session id: %s", loaded.ID)
	}

	if err := store.Delete(context.Background(), session.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	_, found, err = store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get after delete failed: %v", err)
	}
	if found {
		t.Fatal("expected session to be deleted")
	}
}

func TestValkeyStore_GetMissing(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{data: map[string]string{}}, "", time.Hour)
	_, found, err := store.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if found {
		t.Fatal("expected missing session")
	}
}

func TestValkeyStore_SaveError(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{setErr: errors.New("set failed")}, "", time.Hour)
	err := store.Save(context.Background(), domain.Session{ID: "session-1"})
	if err == nil {
		t.Fatal("expected save error")
	}
}

func TestValkeyStore_DeleteError(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{delErr: errors.New("del failed")}, "", time.Hour)
	err := store.Delete(context.Background(), "session-1")
	if err == nil {
		t.Fatal("expected delete error")
	}
}

func TestValkeyStore_GetInvalidJSON(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{data: map[string]string{
		"workspace:test:session:session-1": "{not-json",
	}}, "workspace:test:session", time.Hour)
	_, _, err := store.Get(context.Background(), "session-1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestValkeyStore_DefaultPrefix(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{}, " ", 0)
	if store.keyPrefix != "workspace:session" {
		t.Fatalf("unexpected key prefix: %s", store.keyPrefix)
	}
}

func TestValkeyStore_ExpiredSessionEvicted(t *testing.T) {
	client := &fakeValkeyClient{data: map[string]string{}}
	store := NewValkeyStore(client, "workspace:test:session", time.Hour)
	expired := domain.Session{
		ID:        "session-expired",
		CreatedAt: time.Now().UTC().Add(-2 * time.Minute),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	payload, _ := json.Marshal(expired)
	client.data[store.key(expired.ID)] = string(payload)

	_, found, err := store.Get(context.Background(), expired.ID)
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if found {
		t.Fatal("expected expired session to be treated as missing")
	}
	if _, exists := client.data[store.key(expired.ID)]; exists {
		t.Fatal("expected expired session key to be deleted")
	}
}

func TestNewValkeyStoreFromAddress_InvalidAddress(t *testing.T) {
	_, err := NewValkeyStoreFromAddress(context.Background(), "127.0.0.1:0", "", 0, "", time.Second)
	if err == nil {
		t.Fatal("expected valkey connection error")
	}
}

func TestTTLFromSessionExpiry(t *testing.T) {
	ttl := ttlFromSessionExpiry(time.Now().UTC().Add(10*time.Second), 0)
	if ttl <= 0 {
		t.Fatalf("expected positive ttl, got %s", ttl)
	}

	ttl = ttlFromSessionExpiry(time.Now().UTC().Add(-10*time.Second), time.Minute)
	if ttl != time.Second {
		t.Fatalf("expected minimum ttl for expired session, got %s", ttl)
	}

	ttl = ttlFromSessionExpiry(time.Time{}, time.Minute)
	if ttl != time.Minute {
		t.Fatalf("expected fallback ttl, got %s", ttl)
	}
}
