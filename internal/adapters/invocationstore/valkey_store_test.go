package invocationstore

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
	data     map[string]string
	pingErr  error
	setErr   error
	setNXErr error
	getErr   error
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

func (f *fakeValkeyClient) SetNX(_ context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	if f.setNXErr != nil {
		return redis.NewBoolResult(false, f.setNXErr)
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	if _, exists := f.data[key]; exists {
		return redis.NewBoolResult(false, nil)
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
	return redis.NewBoolResult(true, nil)
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

func TestValkeyStore_SaveAndGet(t *testing.T) {
	client := &fakeValkeyClient{data: map[string]string{}}
	store := NewValkeyStore(client, "workspace:test", time.Hour)

	invocation := domain.Invocation{
		ID:        "inv-1",
		SessionID: "session-1",
		ToolName:  "fs.read",
		Status:    domain.InvocationStatusSucceeded,
		StartedAt: time.Now().UTC(),
		Output:    map[string]any{"content": "ok"},
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: "hello"}},
	}

	if err := store.Save(context.Background(), invocation); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	stored, found, err := store.Get(context.Background(), invocation.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatalf("expected invocation to be found")
	}
	if stored.ID != invocation.ID {
		t.Fatalf("unexpected invocation id: %s", stored.ID)
	}
	if stored.Output != nil || len(stored.Logs) != 0 {
		t.Fatalf("expected valkey envelope without output/logs, got output=%#v logs=%#v", stored.Output, stored.Logs)
	}
}

func TestValkeyStore_GetMissing(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{data: map[string]string{}}, "", 0)
	_, found, err := store.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if found {
		t.Fatalf("expected missing invocation")
	}
}

func TestValkeyStore_SaveErrors(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{setErr: errors.New("set failed")}, "workspace:test", 0)
	err := store.Save(context.Background(), domain.Invocation{ID: "inv-1"})
	if err == nil {
		t.Fatalf("expected save error")
	}
}

func TestValkeyStore_SaveSetNXError(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{setNXErr: errors.New("setnx failed")}, "workspace:test", 0)
	err := store.Save(context.Background(), domain.Invocation{
		ID:            "inv-1",
		SessionID:     "session-1",
		ToolName:      "fs.read",
		CorrelationID: "corr-1",
	})
	if err == nil {
		t.Fatalf("expected save error when correlation index fails")
	}
}

func TestValkeyStore_GetErrors(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{getErr: errors.New("get failed")}, "workspace:test", 0)
	_, _, err := store.Get(context.Background(), "inv-1")
	if err == nil {
		t.Fatalf("expected get error")
	}
}

func TestValkeyStore_GetInvalidJSON(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{
		data: map[string]string{"workspace:test:inv-bad": "{not-json"},
	}, "workspace:test", 0)

	_, _, err := store.Get(context.Background(), "inv-bad")
	if err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

func TestValkeyStore_DefaultPrefix(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{}, " ", 0)
	if store.keyPrefix != "workspace:invocation" {
		t.Fatalf("expected default prefix, got %s", store.keyPrefix)
	}
}

func TestNewValkeyStoreFromAddress_InvalidAddress(t *testing.T) {
	_, err := NewValkeyStoreFromAddress(
		context.Background(),
		"127.0.0.1:0",
		"",
		0,
		"",
		time.Second,
	)
	if err == nil {
		t.Fatalf("expected connection error")
	}
}

func TestValkeyStore_Key(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{}, "workspace:test", 0)
	if key := store.key("inv-1"); key != "workspace:test:inv-1" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestValkeyStore_FindByCorrelation(t *testing.T) {
	client := &fakeValkeyClient{data: map[string]string{}}
	store := NewValkeyStore(client, "workspace:test", 0)
	invocation := domain.Invocation{
		ID:            "inv-1",
		SessionID:     "session-1",
		ToolName:      "fs.read",
		CorrelationID: "corr-1",
		Status:        domain.InvocationStatusSucceeded,
		StartedAt:     time.Now().UTC(),
	}
	if err := store.Save(context.Background(), invocation); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	foundInvocation, found, err := store.FindByCorrelation(context.Background(), "session-1", "fs.read", "corr-1")
	if err != nil {
		t.Fatalf("find by correlation failed: %v", err)
	}
	if !found {
		t.Fatalf("expected invocation to be found")
	}
	if foundInvocation.ID != invocation.ID {
		t.Fatalf("unexpected invocation: %#v", foundInvocation)
	}
}
