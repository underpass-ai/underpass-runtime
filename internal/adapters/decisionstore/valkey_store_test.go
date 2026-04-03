package decisionstore

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
	data   map[string]string
	setErr error
	getErr error
}

func (f *fakeValkeyClient) Ping(_ context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", nil)
}

func (f *fakeValkeyClient) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	if f.setErr != nil {
		return redis.NewStatusResult("", f.setErr)
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	switch v := value.(type) {
	case []byte:
		f.data[key] = string(v)
	case string:
		f.data[key] = v
	default:
		payload, _ := json.Marshal(v)
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

func TestValkeyStore_SaveAndGet(t *testing.T) {
	client := &fakeValkeyClient{data: map[string]string{}}
	store := NewValkeyStore(client, "workspace:decision", 24*time.Hour)

	decision := domain.RecommendationDecision{
		RecommendationID: "rec-1",
		SessionID:        "sess-1",
		TenantID:         "tenant-1",
		ActorID:          "actor-1",
		TaskHint:         "read a file",
		TopK:             5,
		DecisionSource:   "heuristic_only",
		AlgorithmID:      "heuristic_v1",
		AlgorithmVersion: "1.0.0",
		PolicyMode:       "none",
		CandidateCount:   10,
		EventID:          "evt-1",
		CreatedAt:        time.Now().UTC(),
		Recommendations: []domain.RankedToolEvidence{
			{ToolID: "fs.read_file", Rank: 1, FinalScore: 0.95, Why: "exact match"},
		},
	}

	if err := store.Save(context.Background(), decision); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, found, err := store.Get(context.Background(), "rec-1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatal("expected decision to be found")
	}
	if got.RecommendationID != "rec-1" {
		t.Fatalf("expected rec-1, got %s", got.RecommendationID)
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("expected sess-1, got %s", got.SessionID)
	}
	if got.DecisionSource != "heuristic_only" {
		t.Fatalf("expected heuristic_only, got %s", got.DecisionSource)
	}
	if len(got.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(got.Recommendations))
	}
	if got.Recommendations[0].ToolID != "fs.read_file" {
		t.Fatalf("expected fs.read_file, got %s", got.Recommendations[0].ToolID)
	}
}

func TestValkeyStore_GetMissing(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{data: map[string]string{}}, "", 0)

	_, found, err := store.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestValkeyStore_SaveError(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{setErr: errors.New("write failed")}, "test", 0)

	err := store.Save(context.Background(), domain.RecommendationDecision{RecommendationID: "rec-1"})
	if err == nil {
		t.Fatal("expected save error")
	}
}

func TestValkeyStore_GetError(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{getErr: errors.New("read failed")}, "test", 0)

	_, _, err := store.Get(context.Background(), "rec-1")
	if err == nil {
		t.Fatal("expected get error")
	}
}

func TestValkeyStore_GetInvalidJSON(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{
		data: map[string]string{"test:rec-bad": "{not-json"},
	}, "test", 0)

	_, _, err := store.Get(context.Background(), "rec-bad")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestValkeyStore_DefaultPrefix(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{}, " ", 0)
	if store.keyPrefix != "workspace:decision" {
		t.Fatalf("expected default prefix, got %s", store.keyPrefix)
	}
}

func TestValkeyStore_Key(t *testing.T) {
	store := NewValkeyStore(&fakeValkeyClient{}, "workspace:decision", 0)
	if key := store.key("rec-123"); key != "workspace:decision:rec-123" {
		t.Fatalf("unexpected key: %s", key)
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
		nil,
	)
	if err == nil {
		t.Fatal("expected connection error")
	}
}
