package tools

import (
	"context"
	"encoding/json"
	"errors"
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
