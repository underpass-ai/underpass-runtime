package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoFindHandler struct {
	client mongoClient
}

type MongoAggregateHandler struct {
	client mongoClient
}

type mongoClient interface {
	Find(ctx context.Context, req mongoFindRequest) ([]map[string]any, error)
	Aggregate(ctx context.Context, req mongoAggregateRequest) ([]map[string]any, error)
}

type mongoFindRequest struct {
	Endpoint   string
	Database   string
	Collection string
	Filter     map[string]any
	Projection map[string]any
	Sort       map[string]any
	Limit      int64
	Timeout    time.Duration
}

type mongoAggregateRequest struct {
	Endpoint   string
	Database   string
	Collection string
	Pipeline   []map[string]any
	Limit      int64
	Timeout    time.Duration
}

type liveMongoClient struct{}

func NewMongoFindHandler(client mongoClient) *MongoFindHandler {
	return &MongoFindHandler{client: ensureMongoClient(client)}
}

func NewMongoAggregateHandler(client mongoClient) *MongoAggregateHandler {
	return &MongoAggregateHandler{client: ensureMongoClient(client)}
}

func (h *MongoFindHandler) Name() string {
	return "mongo.find"
}

func (h *MongoFindHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID  string         `json:"profile_id"`
		Database   string         `json:"database"`
		Collection string         `json:"collection"`
		Filter     map[string]any `json:"filter"`
		Projection map[string]any `json:"projection"`
		Sort       map[string]any `json:"sort"`
		Limit      int            `json:"limit"`
		MaxBytes   int            `json:"max_bytes"`
		TimeoutMS  int            `json:"timeout_ms"`
	}{
		Filter:    map[string]any{},
		Limit:     50,
		MaxBytes:  262144,
		TimeoutMS: 3000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid mongo.find args",
				Retryable: false,
			}
		}
	}

	database := strings.TrimSpace(request.Database)
	collection := strings.TrimSpace(request.Collection)
	if database == "" || collection == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "database and collection are required",
			Retryable: false,
		}
	}
	limit := int64(clampInt(request.Limit, 1, 200, 50))
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 262144)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 3000)

	profile, endpoint, profileErr := resolveMongoProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !databaseAllowedByProfile(database, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "database outside profile allowlist",
			Retryable: false,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	docs, err := h.client.Find(runCtx, mongoFindRequest{
		Endpoint:   endpoint,
		Database:   database,
		Collection: collection,
		Filter:     request.Filter,
		Projection: request.Projection,
		Sort:       request.Sort,
		Limit:      limit,
		Timeout:    time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("mongo find failed: %v", err),
			Retryable: true,
		}
	}

	outDocs, totalBytes, truncated := truncateMongoDocuments(docs, maxBytes)
	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "mongo find completed",
		}},
		Output: map[string]any{
			"profile_id":      profile.ID,
			"database":        database,
			"collection":      collection,
			"documents":       outDocs,
			"document_count":  len(outDocs),
			"effective_limit": limit,
			"total_bytes":     totalBytes,
			"truncated":       truncated,
		},
	}, nil
}

func (h *MongoAggregateHandler) Name() string {
	return "mongo.aggregate"
}

func (h *MongoAggregateHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID  string           `json:"profile_id"`
		Database   string           `json:"database"`
		Collection string           `json:"collection"`
		Pipeline   []map[string]any `json:"pipeline"`
		Limit      int              `json:"limit"`
		MaxBytes   int              `json:"max_bytes"`
		TimeoutMS  int              `json:"timeout_ms"`
	}{
		Pipeline:  []map[string]any{},
		Limit:     50,
		MaxBytes:  262144,
		TimeoutMS: 3000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid mongo.aggregate args",
				Retryable: false,
			}
		}
	}

	database := strings.TrimSpace(request.Database)
	collection := strings.TrimSpace(request.Collection)
	if database == "" || collection == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "database and collection are required",
			Retryable: false,
		}
	}
	limit := int64(clampInt(request.Limit, 1, 200, 50))
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 262144)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 3000)

	profile, endpoint, profileErr := resolveMongoProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !databaseAllowedByProfile(database, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "database outside profile allowlist",
			Retryable: false,
		}
	}

	pipeline := make([]map[string]any, 0, len(request.Pipeline)+1)
	pipeline = append(pipeline, request.Pipeline...)
	// Server-side hard limit to keep results deterministic and bounded.
	pipeline = append(pipeline, map[string]any{"$limit": limit})

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	docs, err := h.client.Aggregate(runCtx, mongoAggregateRequest{
		Endpoint:   endpoint,
		Database:   database,
		Collection: collection,
		Pipeline:   pipeline,
		Limit:      limit,
		Timeout:    time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("mongo aggregate failed: %v", err),
			Retryable: true,
		}
	}

	outDocs, totalBytes, truncated := truncateMongoDocuments(docs, maxBytes)
	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "mongo aggregate completed",
		}},
		Output: map[string]any{
			"profile_id":      profile.ID,
			"database":        database,
			"collection":      collection,
			"documents":       outDocs,
			"document_count":  len(outDocs),
			"effective_limit": limit,
			"total_bytes":     totalBytes,
			"truncated":       truncated,
		},
	}, nil
}

func ensureMongoClient(client mongoClient) mongoClient {
	if client != nil {
		return client
	}
	return &liveMongoClient{}
}

func (c *liveMongoClient) Find(ctx context.Context, req mongoFindRequest) ([]map[string]any, error) {
	client, closeFn, err := openMongoClient(req.Endpoint, req.Timeout)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	coll := client.Database(req.Database).Collection(req.Collection)
	opts := options.Find().SetLimit(req.Limit)
	if len(req.Projection) > 0 {
		opts.SetProjection(req.Projection)
	}
	if len(req.Sort) > 0 {
		opts.SetSort(req.Sort)
	}
	filter := req.Filter
	if filter == nil {
		filter = map[string]any{}
	}

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	out := make([]map[string]any, 0, req.Limit)
	for cursor.Next(ctx) {
		var doc bson.M
		if decodeErr := cursor.Decode(&doc); decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, map[string]any(doc))
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *liveMongoClient) Aggregate(ctx context.Context, req mongoAggregateRequest) ([]map[string]any, error) {
	client, closeFn, err := openMongoClient(req.Endpoint, req.Timeout)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	coll := client.Database(req.Database).Collection(req.Collection)
	pipeline := req.Pipeline
	if pipeline == nil {
		pipeline = []map[string]any{}
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	out := make([]map[string]any, 0, req.Limit)
	for cursor.Next(ctx) {
		var doc bson.M
		if decodeErr := cursor.Decode(&doc); decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, map[string]any(doc))
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func openMongoClient(endpoint string, timeout time.Duration) (*mongo.Client, func(), error) {
	candidate := strings.TrimSpace(endpoint)
	if candidate == "" {
		return nil, func() { /* no-op cleanup */ }, fmt.Errorf("mongo endpoint is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(candidate))
	if err != nil {
		return nil, func() { /* no-op cleanup */ }, err
	}
	closeFn := func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer disconnectCancel()
		_ = client.Disconnect(disconnectCtx)
	}
	return client, closeFn, nil
}

func resolveMongoProfile(session domain.Session, requestedProfileID string) (connectionProfile, string, *domain.Error) {
	return resolveTypedProfile(session, requestedProfileID,
		[]string{"mongo", "mongodb"}, "dev.mongo",
		"mongodb://localhost:27017")
}

func databaseAllowedByProfile(database string, profile connectionProfile) bool {
	allowlist := extractProfileStringList(profile.Scopes, "databases")
	if len(allowlist) == 0 {
		return false
	}

	database = strings.TrimSpace(database)
	for _, allowed := range allowlist {
		allowed = strings.TrimSpace(allowed)
		if allowed == "*" || allowed == database {
			return true
		}
		if strings.Contains(allowed, "*") {
			parts := strings.Split(allowed, "*")
			if len(parts) == 2 && strings.HasPrefix(database, parts[0]) && strings.HasSuffix(database, parts[1]) {
				return true
			}
		}
	}
	return false
}

func truncateMongoDocuments(docs []map[string]any, maxBytes int) ([]map[string]any, int, bool) {
	out := make([]map[string]any, 0, len(docs))
	total := 0
	truncated := false

	for _, doc := range docs {
		encoded, err := json.Marshal(doc)
		if err != nil {
			encoded = []byte(`{"_warning":"unserializable_document"}`)
		}
		if total+len(encoded) > maxBytes {
			truncated = true
			break
		}
		total += len(encoded)
		out = append(out, doc)
	}
	return out, total, truncated
}
