package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeMongoClient struct {
	find      func(req mongoFindRequest) ([]map[string]any, error)
	aggregate func(req mongoAggregateRequest) ([]map[string]any, error)
}

func (f *fakeMongoClient) Find(_ context.Context, req mongoFindRequest) ([]map[string]any, error) {
	if f.find != nil {
		return f.find(req)
	}
	return []map[string]any{}, nil
}

func (f *fakeMongoClient) Aggregate(_ context.Context, req mongoAggregateRequest) ([]map[string]any, error) {
	if f.aggregate != nil {
		return f.aggregate(req)
	}
	return []map[string]any{}, nil
}

func TestMongoFindHandler_Success(t *testing.T) {
	handler := NewMongoFindHandler(&fakeMongoClient{
		find: func(req mongoFindRequest) ([]map[string]any, error) {
			if req.Database != "sandbox" || req.Collection != "todos" {
				t.Fatalf("unexpected mongo.find request: %#v", req)
			}
			return []map[string]any{
				{"id": 1, "title": "a"},
				{"id": 2, "title": "b"},
			}, nil
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.mongo","database":"sandbox","collection":"todos","limit":10}`))
	if err != nil {
		t.Fatalf("unexpected mongo.find error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["document_count"] != 2 {
		t.Fatalf("unexpected document_count: %#v", output["document_count"])
	}
}

func TestMongoFindHandler_DeniesDatabaseOutsideProfileScopes(t *testing.T) {
	handler := NewMongoFindHandler(&fakeMongoClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.mongo","database":"prod","collection":"todos"}`))
	if err == nil {
		t.Fatal("expected database policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestMongoAggregateHandler_Success(t *testing.T) {
	handler := NewMongoAggregateHandler(&fakeMongoClient{
		aggregate: func(req mongoAggregateRequest) ([]map[string]any, error) {
			if req.Database != "sandbox" || req.Collection != "todos" {
				t.Fatalf("unexpected mongo.aggregate request: %#v", req)
			}
			return []map[string]any{
				{"status": "done", "count": 2},
			}, nil
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.mongo","database":"sandbox","collection":"todos","pipeline":[{"$match":{"status":"done"}}],"limit":5}`))
	if err != nil {
		t.Fatalf("unexpected mongo.aggregate error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["document_count"] != 1 {
		t.Fatalf("unexpected document_count: %#v", output["document_count"])
	}
}

func TestMongoAggregateHandler_MapsExecutionErrors(t *testing.T) {
	handler := NewMongoAggregateHandler(&fakeMongoClient{
		aggregate: func(req mongoAggregateRequest) ([]map[string]any, error) {
			return nil, errors.New("dial failed")
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.mongo","database":"sandbox","collection":"todos","pipeline":[]}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestMongoHandlers_NamesAndLiveClientErrors(t *testing.T) {
	if NewMongoFindHandler(nil).Name() != "mongo.find" {
		t.Fatal("unexpected mongo.find name")
	}
	if NewMongoAggregateHandler(nil).Name() != "mongo.aggregate" {
		t.Fatal("unexpected mongo.aggregate name")
	}

	client := &liveMongoClient{}
	ctx := context.Background()
	_, err := client.Find(ctx, mongoFindRequest{
		Endpoint:   "",
		Database:   "sandbox",
		Collection: "todos",
		Limit:      1,
	})
	if err == nil {
		t.Fatal("expected live mongo Find endpoint validation error")
	}
	_, err = client.Aggregate(ctx, mongoAggregateRequest{
		Endpoint:   "",
		Database:   "sandbox",
		Collection: "todos",
		Limit:      1,
	})
	if err == nil {
		t.Fatal("expected live mongo Aggregate endpoint validation error")
	}
}

func TestMongoHelpers_ProfileAndDatabasePolicies(t *testing.T) {
	_, _, err := resolveMongoProfile(domain.Session{}, "")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected profile_id validation error, got %#v", err)
	}

	sessionWrongKind := domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"x","kind":"nats","read_only":true,"scopes":{"databases":["sandbox"]}}]`,
		},
	}
	_, _, err = resolveMongoProfile(sessionWrongKind, "x")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected wrong kind error, got %#v", err)
	}

	if _, _, err := openMongoClient("", 10); err == nil {
		t.Fatal("expected openMongoClient endpoint validation error")
	}

	// Validate that liveMongoClient.Find rejects dangerous filters.
	liveClient := &liveMongoClient{}
	_, findErr := liveClient.Find(context.Background(), mongoFindRequest{
		Endpoint:   "mongodb://localhost:27017",
		Database:   "test",
		Collection: "col",
		Filter:     map[string]any{"$where": "evil()"},
		Limit:      1,
		Timeout:    1,
	})
	if findErr == nil || !strings.Contains(findErr.Error(), "$where") {
		t.Fatalf("expected $where rejection from liveMongoClient.Find, got: %v", findErr)
	}

	// Validate that liveMongoClient.Aggregate rejects dangerous pipeline stages.
	_, aggErr := liveClient.Aggregate(context.Background(), mongoAggregateRequest{
		Endpoint:   "mongodb://localhost:27017",
		Database:   "test",
		Collection: "col",
		Pipeline:   []map[string]any{{"$function": "evil()"}},
		Limit:      1,
		Timeout:    1,
	})
	if aggErr == nil || !strings.Contains(aggErr.Error(), "$function") {
		t.Fatalf("expected $function rejection from liveMongoClient.Aggregate, got: %v", aggErr)
	}

	profile := connectionProfile{Scopes: map[string]any{"databases": []any{"sandbox", "dev*"}}}
	if !databaseAllowedByProfile("sandbox", profile) {
		t.Fatal("expected exact database allow")
	}
	if !databaseAllowedByProfile("dev-tenant", profile) {
		t.Fatal("expected wildcard database allow")
	}
	if databaseAllowedByProfile("prod", profile) {
		t.Fatal("expected database deny")
	}
}

func TestValidateMongoFilter_SafeOperators(t *testing.T) {
	safe := map[string]any{
		"status": "active",
		"$and": []any{
			map[string]any{"age": map[string]any{"$gte": 18}},
			map[string]any{"role": "admin"},
		},
	}
	if err := validateMongoFilter(safe); err != nil {
		t.Fatalf("expected safe filter to pass: %v", err)
	}
}

func TestValidateMongoFilter_DangerousOperators(t *testing.T) {
	for _, op := range mongoDangerousOperators {
		filter := map[string]any{op: "malicious()"}
		if err := validateMongoFilter(filter); err == nil {
			t.Fatalf("expected %s to be rejected", op)
		}
	}
}

func TestValidateMongoFilter_NestedDangerous(t *testing.T) {
	nested := map[string]any{
		"status": map[string]any{
			"$where": "this.isAdmin()",
		},
	}
	if err := validateMongoFilter(nested); err == nil {
		t.Fatal("expected nested $where to be rejected")
	}
}

func TestValidateMongoFilter_EmptyFilter(t *testing.T) {
	if err := validateMongoFilter(map[string]any{}); err != nil {
		t.Fatalf("expected empty filter to pass: %v", err)
	}
}

func TestValidateMongoFilter_DeepNested(t *testing.T) {
	deep := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"$function": "evil()",
			},
		},
	}
	if err := validateMongoFilter(deep); err == nil {
		t.Fatal("expected deeply nested $function to be rejected")
	}
}

func TestValidateMongoFilter_SafeComplex(t *testing.T) {
	complex := map[string]any{
		"$or": []any{
			map[string]any{"status": "active"},
			map[string]any{"status": "pending"},
		},
		"$and": []any{
			map[string]any{"age": map[string]any{"$gte": 18}},
			map[string]any{"country": map[string]any{"$in": []any{"US", "UK"}}},
		},
		"name": map[string]any{"$regex": "^test"},
	}
	if err := validateMongoFilter(complex); err != nil {
		t.Fatalf("expected complex safe filter to pass: %v", err)
	}
}

func TestValidateMongoFilter_AllDangerousRejected(t *testing.T) {
	for _, op := range []string{"$where", "$accumulator", "$function", "$expr"} {
		// Top-level
		if err := validateMongoFilter(map[string]any{op: "val"}); err == nil {
			t.Errorf("expected top-level %s to be rejected", op)
		}
		// Nested
		if err := validateMongoFilter(map[string]any{
			"wrapper": map[string]any{op: "val"},
		}); err == nil {
			t.Errorf("expected nested %s to be rejected", op)
		}
	}
}
