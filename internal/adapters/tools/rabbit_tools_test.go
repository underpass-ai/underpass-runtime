package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeRabbitClient struct {
	consume   func(req rabbitConsumeRequest) ([]rabbitConsumedMessage, error)
	publish   func(req rabbitPublishRequest) error
	queueInfo func(req rabbitQueueInfoRequest) (rabbitQueueInfo, error)
}

func (f *fakeRabbitClient) Consume(_ context.Context, req rabbitConsumeRequest) ([]rabbitConsumedMessage, error) {
	if f.consume != nil {
		return f.consume(req)
	}
	return []rabbitConsumedMessage{}, nil
}

func (f *fakeRabbitClient) QueueInfo(_ context.Context, req rabbitQueueInfoRequest) (rabbitQueueInfo, error) {
	if f.queueInfo != nil {
		return f.queueInfo(req)
	}
	return rabbitQueueInfo{Name: req.Queue}, nil
}

func (f *fakeRabbitClient) Publish(_ context.Context, req rabbitPublishRequest) error {
	if f.publish != nil {
		return f.publish(req)
	}
	return nil
}

func TestRabbitConsumeHandler_Success(t *testing.T) {
	handler := NewRabbitConsumeHandler(&fakeRabbitClient{
		consume: func(req rabbitConsumeRequest) ([]rabbitConsumedMessage, error) {
			if req.URL == "" || req.Queue != "sandbox.jobs" || req.Timeout <= 0 {
				t.Fatalf("unexpected consume request: %#v", req)
			}
			return []rabbitConsumedMessage{
				{
					Body:        []byte("hello"),
					Exchange:    "events",
					RoutingKey:  "sandbox.jobs",
					Redelivered: false,
					Timestamp:   time.Unix(1700000000, 0),
				},
			}, nil
		},
	})

	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs","max_messages":1}`))
	if err != nil {
		t.Fatalf("unexpected rabbit.consume error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["profile_id"] != "dev.rabbit" {
		t.Fatalf("unexpected profile_id: %#v", output["profile_id"])
	}
	if output["queue"] != "sandbox.jobs" {
		t.Fatalf("unexpected queue: %#v", output["queue"])
	}
}

func TestRabbitConsumeHandler_DeniesQueueOutsideProfileScopes(t *testing.T) {
	handler := NewRabbitConsumeHandler(&fakeRabbitClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.rabbit","queue":"prod.jobs"}`))
	if err == nil {
		t.Fatal("expected queue policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRabbitPublishHandler_Success(t *testing.T) {
	handler := NewRabbitPublishHandler(&fakeRabbitClient{
		publish: func(req rabbitPublishRequest) error {
			if req.URL == "" || req.Exchange != "events" || req.RoutingKey != "sandbox.jobs" || req.Timeout <= 0 {
				t.Fatalf("unexpected publish request: %#v", req)
			}
			if string(req.Payload) != "hello" {
				t.Fatalf("unexpected payload: %q", string(req.Payload))
			}
			return nil
		},
	})

	result, err := handler.Invoke(
		context.Background(),
		writableRabbitSession(),
		json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs","exchange":"events","payload":"hello"}`),
	)
	if err != nil {
		t.Fatalf("unexpected rabbit.publish error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["published"] != true {
		t.Fatalf("expected published=true, got %#v", output["published"])
	}
}

func TestRabbitPublishHandler_DeniesReadOnlyProfile(t *testing.T) {
	handler := NewRabbitPublishHandler(&fakeRabbitClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs","payload":"hello"}`))
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

func TestRabbitPublishHandler_ExecutionError(t *testing.T) {
	handler := NewRabbitPublishHandler(&fakeRabbitClient{
		publish: func(req rabbitPublishRequest) error {
			return errors.New("dial failed")
		},
	})

	_, err := handler.Invoke(context.Background(), writableRabbitSession(), json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs","payload":"hello"}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRabbitQueueInfoHandler_Success(t *testing.T) {
	handler := NewRabbitQueueInfoHandler(&fakeRabbitClient{
		queueInfo: func(req rabbitQueueInfoRequest) (rabbitQueueInfo, error) {
			if req.Queue != "sandbox.jobs" {
				t.Fatalf("unexpected queue: %s", req.Queue)
			}
			return rabbitQueueInfo{Name: req.Queue, Messages: 5, Consumers: 2}, nil
		},
	})

	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs"}`))
	if err != nil {
		t.Fatalf("unexpected rabbit.queue_info error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	if output["messages"] != 5 {
		t.Fatalf("unexpected messages count: %#v", output["messages"])
	}
}

func TestRabbitQueueInfoHandler_MapsExecutionErrors(t *testing.T) {
	handler := NewRabbitQueueInfoHandler(&fakeRabbitClient{
		queueInfo: func(req rabbitQueueInfoRequest) (rabbitQueueInfo, error) {
			return rabbitQueueInfo{}, errors.New("dial failed")
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.rabbit","queue":"sandbox.jobs"}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestRabbitHandlers_NamesAndLiveClientErrors(t *testing.T) {
	if NewRabbitConsumeHandler(nil).Name() != "rabbit.consume" {
		t.Fatal("unexpected rabbit.consume name")
	}
	if NewRabbitPublishHandler(nil).Name() != "rabbit.publish" {
		t.Fatal("unexpected rabbit.publish name")
	}
	if NewRabbitQueueInfoHandler(nil).Name() != "rabbit.queue_info" {
		t.Fatal("unexpected rabbit.queue_info name")
	}

	client := &liveRabbitClient{}
	ctx := context.Background()
	_, err := client.QueueInfo(ctx, rabbitQueueInfoRequest{
		URL:     "amqp://invalid:5672",
		Queue:   "sandbox.jobs",
		Timeout: 5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected live rabbit QueueInfo connection error")
	}

	_, err = client.Consume(ctx, rabbitConsumeRequest{
		URL:         "amqp://invalid:5672",
		Queue:       "sandbox.jobs",
		MaxMessages: 1,
		Timeout:     5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected live rabbit Consume connection error")
	}

	err = client.Publish(ctx, rabbitPublishRequest{
		URL:        "amqp://invalid:5672",
		Exchange:   "events",
		RoutingKey: "sandbox.jobs",
		Payload:    []byte("hello"),
		Timeout:    5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected live rabbit Publish connection error")
	}

	if _, _, _, err = openRabbitChannel("amqp://invalid:5672", 5*time.Millisecond); err == nil {
		t.Fatal("expected openRabbitChannel connection error")
	}
}

func TestRabbitProfileAndQueueHelpers(t *testing.T) {
	_, _, err := resolveRabbitProfile(domain.Session{}, "")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected profile_id validation error, got %#v", err)
	}

	sessionWrongKind := domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"x","kind":"nats","read_only":true,"scopes":{"queues":["sandbox."]}}]`,
		},
	}
	_, _, err = resolveRabbitProfile(sessionWrongKind, "x")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected wrong kind error, got %#v", err)
	}

	profile := connectionProfile{Scopes: map[string]any{"queues": []any{"sandbox.", "dev.*"}}}
	if !queueAllowedByProfile("sandbox.jobs", profile) {
		t.Fatal("expected queueAllowedByProfile allow")
	}
	if queueAllowedByProfile("prod.jobs", profile) {
		t.Fatal("expected queueAllowedByProfile deny")
	}
	if !queuePatternMatch("sandbox.*.dlq", "sandbox.jobs.dlq") {
		t.Fatal("expected queuePatternMatch wildcard allow")
	}
	if queuePatternMatch("sandbox.", "prod.jobs") {
		t.Fatal("expected queuePatternMatch deny")
	}
}

func writableRabbitSession() domain.Session {
	return domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"dev.rabbit","kind":"rabbit","read_only":false,"scopes":{"queues":["sandbox.","dev."]}}]`,
		},
	}
}
