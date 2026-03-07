package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	errRabbitQueueRequired        = "queue is required"
	errRabbitQueueOutsideAllowlist = "queue outside profile allowlist"
)

type RabbitConsumeHandler struct {
	client rabbitClient
}

type RabbitPublishHandler struct {
	client rabbitClient
}

type RabbitQueueInfoHandler struct {
	client rabbitClient
}

type rabbitClient interface {
	Consume(ctx context.Context, req rabbitConsumeRequest) ([]rabbitConsumedMessage, error)
	Publish(ctx context.Context, req rabbitPublishRequest) error
	QueueInfo(ctx context.Context, req rabbitQueueInfoRequest) (rabbitQueueInfo, error)
}

type rabbitConsumeRequest struct {
	URL         string
	Queue       string
	MaxMessages int
	Timeout     time.Duration
}

type rabbitQueueInfoRequest struct {
	URL     string
	Queue   string
	Timeout time.Duration
}

type rabbitPublishRequest struct {
	URL        string
	Exchange   string
	RoutingKey string
	Payload    []byte
	Timeout    time.Duration
}

type rabbitConsumedMessage struct {
	Body        []byte
	Exchange    string
	RoutingKey  string
	Redelivered bool
	Timestamp   time.Time
}

type rabbitQueueInfo struct {
	Name      string
	Messages  int
	Consumers int
}

type liveRabbitClient struct{}

func NewRabbitConsumeHandler(client rabbitClient) *RabbitConsumeHandler {
	return &RabbitConsumeHandler{client: ensureRabbitClient(client)}
}

func NewRabbitPublishHandler(client rabbitClient) *RabbitPublishHandler {
	return &RabbitPublishHandler{client: ensureRabbitClient(client)}
}

func NewRabbitQueueInfoHandler(client rabbitClient) *RabbitQueueInfoHandler {
	return &RabbitQueueInfoHandler{client: ensureRabbitClient(client)}
}

func (h *RabbitConsumeHandler) Name() string {
	return "rabbit.consume"
}

func (h *RabbitPublishHandler) Name() string {
	return "rabbit.publish"
}

func (h *RabbitConsumeHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID   string `json:"profile_id"`
		Queue       string `json:"queue"`
		MaxMessages int    `json:"max_messages"`
		MaxBytes    int    `json:"max_bytes"`
		TimeoutMS   int    `json:"timeout_ms"`
	}{
		MaxMessages: 20,
		MaxBytes:    262144,
		TimeoutMS:   2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid rabbit.consume args",
				Retryable: false,
			}
		}
	}

	queue := strings.TrimSpace(request.Queue)
	if queue == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRabbitQueueRequired,
			Retryable: false,
		}
	}

	maxMessages := clampInt(request.MaxMessages, 1, 200, 20)
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 262144)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRabbitProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !queueAllowedByProfile(queue, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRabbitQueueOutsideAllowlist,
			Retryable: false,
		}
	}

	messages, err := h.client.Consume(ctx, rabbitConsumeRequest{
		URL:         endpoint,
		Queue:       queue,
		MaxMessages: maxMessages,
		Timeout:     time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("rabbit consume failed: %v", err),
			Retryable: true,
		}
	}

	outMessages := make([]map[string]any, 0, len(messages))
	totalBytes := 0
	truncated := false
	for _, message := range messages {
		if totalBytes >= maxBytes {
			truncated = true
			break
		}

		body := message.Body
		bodyTrimmed := false
		remaining := maxBytes - totalBytes
		if len(body) > remaining {
			body = body[:remaining]
			bodyTrimmed = true
			truncated = true
		}
		totalBytes += len(body)

		outMessages = append(outMessages, map[string]any{
			"exchange":       message.Exchange,
			"routing_key":    message.RoutingKey,
			"redelivered":    message.Redelivered,
			"timestamp_unix": message.Timestamp.Unix(),
			"body_base64":    base64.StdEncoding.EncodeToString(body),
			"size_bytes":     len(body),
			"body_trimmed":   bodyTrimmed,
		})
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "rabbit consume completed",
		}},
		Output: map[string]any{
			"profile_id":    profile.ID,
			"queue":         queue,
			"messages":      outMessages,
			"message_count": len(outMessages),
			"total_bytes":   totalBytes,
			"truncated":     truncated,
		},
	}, nil
}

func (h *RabbitPublishHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID       string `json:"profile_id"`
		Queue           string `json:"queue"`
		Exchange        string `json:"exchange"`
		RoutingKey      string `json:"routing_key"`
		Payload         string `json:"payload"`
		PayloadEncoding string `json:"payload_encoding"`
		TimeoutMS       int    `json:"timeout_ms"`
		MaxBytes        int    `json:"max_bytes"`
	}{
		PayloadEncoding: "utf8",
		TimeoutMS:       2000,
		MaxBytes:        1024 * 1024,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid rabbit.publish args",
				Retryable: false,
			}
		}
	}

	queue := strings.TrimSpace(request.Queue)
	if queue == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRabbitQueueRequired,
			Retryable: false,
		}
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 1024*1024)

	profile, endpoint, profileErr := resolveRabbitProfile(session, request.ProfileID)
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
	if !queueAllowedByProfile(queue, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRabbitQueueOutsideAllowlist,
			Retryable: false,
		}
	}

	payloadBytes, payloadErr := decodePayload(request.Payload, request.PayloadEncoding)
	if payloadErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   payloadErr.Error(),
			Retryable: false,
		}
	}
	if len(payloadBytes) > maxBytes {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "payload exceeds max_bytes",
			Retryable: false,
		}
	}

	routingKey := strings.TrimSpace(request.RoutingKey)
	if routingKey == "" {
		routingKey = queue
	}
	exchange := strings.TrimSpace(request.Exchange)

	if err := h.client.Publish(ctx, rabbitPublishRequest{
		URL:        endpoint,
		Exchange:   exchange,
		RoutingKey: routingKey,
		Payload:    payloadBytes,
		Timeout:    time.Duration(timeoutMS) * time.Millisecond,
	}); err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("rabbit publish failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "rabbit publish completed",
		}},
		Output: map[string]any{
			"profile_id":    profile.ID,
			"queue":         queue,
			"exchange":      exchange,
			"routing_key":   routingKey,
			"payload_bytes": len(payloadBytes),
			"published":     true,
		},
	}, nil
}

func (h *RabbitQueueInfoHandler) Name() string {
	return "rabbit.queue_info"
}

func (h *RabbitQueueInfoHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
		Queue     string `json:"queue"`
		TimeoutMS int    `json:"timeout_ms"`
	}{
		TimeoutMS: 2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid rabbit.queue_info args",
				Retryable: false,
			}
		}
	}

	queue := strings.TrimSpace(request.Queue)
	if queue == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errRabbitQueueRequired,
			Retryable: false,
		}
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)

	profile, endpoint, profileErr := resolveRabbitProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !queueAllowedByProfile(queue, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errRabbitQueueOutsideAllowlist,
			Retryable: false,
		}
	}

	info, err := h.client.QueueInfo(ctx, rabbitQueueInfoRequest{
		URL:     endpoint,
		Queue:   queue,
		Timeout: time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("rabbit queue_info failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "rabbit queue_info completed",
		}},
		Output: map[string]any{
			"profile_id": profile.ID,
			"queue":      info.Name,
			"messages":   info.Messages,
			"consumers":  info.Consumers,
		},
	}, nil
}

func ensureRabbitClient(client rabbitClient) rabbitClient {
	if client != nil {
		return client
	}
	return &liveRabbitClient{}
}

func (c *liveRabbitClient) QueueInfo(ctx context.Context, req rabbitQueueInfoRequest) (rabbitQueueInfo, error) {
	conn, ch, closeFn, err := openRabbitChannel(req.URL, req.Timeout)
	if err != nil {
		return rabbitQueueInfo{}, err
	}
	defer closeFn()
	_ = conn

	queue, err := ch.QueueInspect(req.Queue)
	if err != nil {
		return rabbitQueueInfo{}, err
	}
	return rabbitQueueInfo{
		Name:      queue.Name,
		Messages:  queue.Messages,
		Consumers: queue.Consumers,
	}, nil
}

func (c *liveRabbitClient) Publish(ctx context.Context, req rabbitPublishRequest) error {
	conn, ch, closeFn, err := openRabbitChannel(req.URL, req.Timeout)
	if err != nil {
		return err
	}
	defer closeFn()
	_ = conn

	pubCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	return ch.PublishWithContext(pubCtx, req.Exchange, req.RoutingKey, false, false, amqp.Publishing{
		ContentType: "application/octet-stream",
		Body:        req.Payload,
		Timestamp:   time.Now().UTC(),
	})
}

func (c *liveRabbitClient) Consume(ctx context.Context, req rabbitConsumeRequest) ([]rabbitConsumedMessage, error) {
	conn, ch, closeFn, err := openRabbitChannel(req.URL, req.Timeout)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	_ = conn

	deliveries, err := ch.Consume(
		req.Queue,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return nil, err
	}

	deadline := time.NewTimer(req.Timeout)
	defer deadline.Stop()

	out := make([]rabbitConsumedMessage, 0, req.MaxMessages)
	for len(out) < req.MaxMessages {
		select {
		case <-ctx.Done():
			return out, nil
		case <-deadline.C:
			return out, nil
		case delivery, ok := <-deliveries:
			if !ok {
				return out, nil
			}
			out = append(out, rabbitConsumedMessage{
				Body:        delivery.Body,
				Exchange:    delivery.Exchange,
				RoutingKey:  delivery.RoutingKey,
				Redelivered: delivery.Redelivered,
				Timestamp:   delivery.Timestamp,
			})
		}
	}
	return out, nil
}

func openRabbitChannel(endpoint string, timeout time.Duration) (*amqp.Connection, *amqp.Channel, func(), error) {
	config := amqp.Config{
		Dial: amqp.DefaultDial(timeout),
	}
	conn, err := amqp.DialConfig(endpoint, config)
	if err != nil {
		return nil, nil, func() { /* no-op cleanup */ }, err
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, func() { /* no-op cleanup */ }, err
	}

	closeFn := func() {
		_ = ch.Close()
		_ = conn.Close()
	}
	return conn, ch, closeFn, nil
}

func resolveRabbitProfile(session domain.Session, requestedProfileID string) (connectionProfile, string, *domain.Error) {
	return resolveTypedProfile(session, requestedProfileID,
		[]string{"rabbitmq", "rabbit"}, "dev.rabbit",
		"amqp://guest:guest@localhost:5672/")
}

func queueAllowedByProfile(queue string, profile connectionProfile) bool {
	raw, found := profile.Scopes["queues"]
	if !found {
		return false
	}

	patterns := make([]string, 0, 2)
	switch typed := raw.(type) {
	case []string:
		patterns = append(patterns, typed...)
	case []any:
		for _, entry := range typed {
			if asString, ok := entry.(string); ok {
				patterns = append(patterns, asString)
			}
		}
	default:
		return false
	}

	for _, pattern := range patterns {
		if queuePatternMatch(pattern, queue) {
			return true
		}
	}
	return false
}

func queuePatternMatch(pattern, queue string) bool {
	pattern = strings.TrimSpace(pattern)
	queue = strings.TrimSpace(queue)
	if pattern == "" || queue == "" {
		return false
	}
	if pattern == "*" || pattern == queue {
		return true
	}
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			return strings.HasPrefix(queue, parts[0]) && strings.HasSuffix(queue, parts[1])
		}
	}
	if strings.HasSuffix(pattern, ".") || strings.HasSuffix(pattern, ":") || strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(queue, pattern)
	}
	return false
}
