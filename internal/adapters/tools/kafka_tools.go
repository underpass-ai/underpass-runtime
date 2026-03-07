package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	errKafkaTopicRequired         = "topic is required"
	errKafkaTopicOutsideAllowlist = "topic outside profile allowlist"
	kafkaKeyProfileID             = "profile_id"
	kafkaKeyTimestamp             = "timestamp"
	kafkaKeyPartition             = "partition"
	kafkaOffsetLatest             = "latest"
	kafkaOffsetEarliest           = "earliest"
	kafkaOffsetAbsolute              = "absolute"
	errKafkaOffsetInvalid            = "offset must be earliest/latest or a non-negative integer"
)

type KafkaConsumeHandler struct {
	client kafkaClient
}

type KafkaProduceHandler struct {
	client kafkaClient
}

type KafkaTopicMetadataHandler struct {
	client kafkaClient
}

type kafkaClient interface {
	Consume(ctx context.Context, req kafkaConsumeRequest) ([]kafkaConsumedMessage, error)
	Produce(ctx context.Context, req kafkaProduceRequest) error
	TopicMetadata(ctx context.Context, req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error)
}

type kafkaConsumeRequest struct {
	Brokers     []string
	Topic       string
	Partition   int
	OffsetMode  string
	OffsetStart int64
	OffsetAt    *time.Time
	MaxMessages int
	Timeout     time.Duration
}

type kafkaTopicMetadataRequest struct {
	Brokers []string
	Topic   string
}

type kafkaProduceRequest struct {
	Brokers   []string
	Topic     string
	Partition int
	Key       []byte
	Value     []byte
	Timeout   time.Duration
}

type kafkaConsumedMessage struct {
	Key       []byte
	Value     []byte
	Partition int
	Offset    int64
	Time      time.Time
}

type kafkaTopicPartitionMetadata struct {
	PartitionID int
	LeaderHost  string
	LeaderPort  int
	ReplicaIDs  []int
	ISRIDs      []int
}

type liveKafkaClient struct{}

func NewKafkaConsumeHandler(client kafkaClient) *KafkaConsumeHandler {
	return &KafkaConsumeHandler{client: ensureKafkaClient(client)}
}

func NewKafkaProduceHandler(client kafkaClient) *KafkaProduceHandler {
	return &KafkaProduceHandler{client: ensureKafkaClient(client)}
}

func NewKafkaTopicMetadataHandler(client kafkaClient) *KafkaTopicMetadataHandler {
	return &KafkaTopicMetadataHandler{client: ensureKafkaClient(client)}
}

func (h *KafkaConsumeHandler) Name() string {
	return "kafka.consume"
}

func (h *KafkaProduceHandler) Name() string {
	return "kafka.produce"
}

func (h *KafkaConsumeHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID   string `json:"profile_id"`
		Topic       string `json:"topic"`
		Partition   int    `json:"partition"`
		OffsetMode  string `json:"offset_mode"`
		Offset      any    `json:"offset"`
		TimestampMS *int64 `json:"timestamp_ms"`
		MaxMessages int    `json:"max_messages"`
		MaxBytes    int    `json:"max_bytes"`
		TimeoutMS   int    `json:"timeout_ms"`
	}{
		Partition:   0,
		OffsetMode:  "",
		MaxMessages: 20,
		MaxBytes:    262144,
		TimeoutMS:   2000,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid kafka.consume args",
				Retryable: false,
			}
		}
	}

	topic := strings.TrimSpace(request.Topic)
	if topic == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errKafkaTopicRequired,
			Retryable: false,
		}
	}
	if request.Partition < 0 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "partition must be >= 0",
			Retryable: false,
		}
	}

	maxMessages := clampInt(request.MaxMessages, 1, 200, 20)
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 262144)
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)
	offsetSpec, offsetErr := resolveKafkaConsumeOffset(request.OffsetMode, request.Offset, request.TimestampMS)
	if offsetErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   offsetErr.Error(),
			Retryable: false,
		}
	}

	profile, brokers, profileErr := resolveKafkaProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !topicAllowedByProfile(topic, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errKafkaTopicOutsideAllowlist,
			Retryable: false,
		}
	}

	messages, err := h.client.Consume(ctx, kafkaConsumeRequest{
		Brokers:     brokers,
		Topic:       topic,
		Partition:   request.Partition,
		OffsetMode:  offsetSpec.Mode,
		OffsetStart: offsetSpec.OffsetStart,
		OffsetAt:    offsetSpec.OffsetAt,
		MaxMessages: maxMessages,
		Timeout:     time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("kafka consume failed: %v", err),
			Retryable: true,
		}
	}

	outMessages, totalBytes, truncated := kafkaConsumeFormatMessages(messages, maxBytes)

	output := map[string]any{
		kafkaKeyProfileID: profile.ID,
		"topic":           topic,
		"offset_mode":     offsetSpec.Mode,
		"messages":        outMessages,
		"message_count":   len(outMessages),
		"total_bytes":     totalBytes,
		"truncated":       truncated,
	}
	if offsetSpec.Mode == kafkaOffsetAbsolute {
		output["offset"] = offsetSpec.OffsetStart
	}
	if offsetSpec.Mode == kafkaKeyTimestamp && request.TimestampMS != nil {
		output["timestamp_ms"] = *request.TimestampMS
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "kafka consume completed",
		}},
		Output: output,
	}, nil
}

// kafkaConsumeFormatMessages converts raw kafka messages to the JSON-ready
// format, applying a byte budget across key and value fields and reporting
// whether any content was truncated.
func kafkaConsumeFormatMessages(messages []kafkaConsumedMessage, maxBytes int) ([]map[string]any, int, bool) {
	out := make([]map[string]any, 0, len(messages))
	totalBytes := 0
	truncated := false
	for _, msg := range messages {
		if totalBytes >= maxBytes {
			truncated = true
			break
		}
		keyBytes, valueBytes, keyTrimmed, valueTrimmed, trimmed := kafkaTrimMessageBytes(msg.Key, msg.Value, maxBytes-totalBytes)
		if trimmed {
			truncated = true
		}
		totalBytes += len(keyBytes) + len(valueBytes)
		out = append(out, map[string]any{
			kafkaKeyPartition: msg.Partition,
			"offset":         msg.Offset,
			"timestamp_unix": msg.Time.Unix(),
			"key_base64":     base64.StdEncoding.EncodeToString(keyBytes),
			"value_base64":   base64.StdEncoding.EncodeToString(valueBytes),
			"size_bytes":     len(keyBytes) + len(valueBytes),
			"key_trimmed":    keyTrimmed,
			"value_trimmed":  valueTrimmed,
		})
	}
	return out, totalBytes, truncated
}

// kafkaTrimMessageBytes trims key/value byte slices so that their combined
// length does not exceed remaining. It reports whether any bytes were dropped.
func kafkaTrimMessageBytes(key, value []byte, remaining int) (trimmedKey, trimmedValue []byte, keyTrimmed, valueTrimmed, anyTrimmed bool) {
	trimmedKey = key
	trimmedValue = value
	if len(key)+len(value) <= remaining {
		return trimmedKey, trimmedValue, false, false, false
	}
	anyTrimmed = true
	if len(key) >= remaining {
		trimmedKey = key[:remaining]
		trimmedValue = []byte{}
		keyTrimmed = len(key) > len(trimmedKey)
		valueTrimmed = len(value) > 0
		return trimmedKey, trimmedValue, keyTrimmed, valueTrimmed, anyTrimmed
	}
	valueRemaining := remaining - len(key)
	if len(value) > valueRemaining {
		trimmedValue = value[:valueRemaining]
		valueTrimmed = true
	}
	return trimmedKey, trimmedValue, false, valueTrimmed, anyTrimmed
}

func (h *KafkaProduceHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID     string `json:"profile_id"`
		Topic         string `json:"topic"`
		Partition     int    `json:"partition"`
		Key           string `json:"key"`
		KeyEncoding   string `json:"key_encoding"`
		Value         string `json:"value"`
		ValueEncoding string `json:"value_encoding"`
		TimeoutMS     int    `json:"timeout_ms"`
		MaxBytes      int    `json:"max_bytes"`
	}{
		Partition:     0,
		KeyEncoding:   "utf8",
		ValueEncoding: "utf8",
		TimeoutMS:     2000,
		MaxBytes:      1024 * 1024,
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid kafka.produce args",
				Retryable: false,
			}
		}
	}

	topic := strings.TrimSpace(request.Topic)
	if topic == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errKafkaTopicRequired,
			Retryable: false,
		}
	}
	if request.Partition < 0 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "partition must be >= 0",
			Retryable: false,
		}
	}
	timeoutMS := clampInt(request.TimeoutMS, 100, 10000, 2000)
	maxBytes := clampInt(request.MaxBytes, 1, 1024*1024, 1024*1024)

	profile, brokers, profileErr := resolveKafkaProfile(session, request.ProfileID)
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
	if !topicAllowedByProfile(topic, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errKafkaTopicOutsideAllowlist,
			Retryable: false,
		}
	}

	keyBytes, keyErr := decodePayload(request.Key, request.KeyEncoding)
	if keyErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid key payload",
			Retryable: false,
		}
	}
	valueBytes, valueErr := decodePayload(request.Value, request.ValueEncoding)
	if valueErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid value payload",
			Retryable: false,
		}
	}
	if len(keyBytes)+len(valueBytes) > maxBytes {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "message exceeds max_bytes",
			Retryable: false,
		}
	}

	if err := h.client.Produce(ctx, kafkaProduceRequest{
		Brokers:   brokers,
		Topic:     topic,
		Partition: request.Partition,
		Key:       keyBytes,
		Value:     valueBytes,
		Timeout:   time.Duration(timeoutMS) * time.Millisecond,
	}); err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("kafka produce failed: %v", err),
			Retryable: true,
		}
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "kafka produce completed",
		}},
		Output: map[string]any{
			kafkaKeyProfileID: profile.ID,
			"topic":           topic,
			kafkaKeyPartition: request.Partition,
			"key_bytes":   len(keyBytes),
			"value_bytes": len(valueBytes),
			"produced":    true,
		},
	}, nil
}

func (h *KafkaTopicMetadataHandler) Name() string {
	return "kafka.topic_metadata"
}

func (h *KafkaTopicMetadataHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
		Topic     string `json:"topic"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid kafka.topic_metadata args",
				Retryable: false,
			}
		}
	}

	topic := strings.TrimSpace(request.Topic)
	if topic == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   errKafkaTopicRequired,
			Retryable: false,
		}
	}

	profile, brokers, profileErr := resolveKafkaProfile(session, request.ProfileID)
	if profileErr != nil {
		return app.ToolRunResult{}, profileErr
	}
	if !topicAllowedByProfile(topic, profile) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   errKafkaTopicOutsideAllowlist,
			Retryable: false,
		}
	}

	partitions, err := h.client.TopicMetadata(ctx, kafkaTopicMetadataRequest{
		Brokers: brokers,
		Topic:   topic,
	})
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("kafka topic metadata failed: %v", err),
			Retryable: true,
		}
	}

	outputPartitions := make([]map[string]any, 0, len(partitions))
	for _, partition := range partitions {
		outputPartitions = append(outputPartitions, map[string]any{
			kafkaKeyPartition: partition.PartitionID,
			"leader_host":   partition.LeaderHost,
			"leader_port":   partition.LeaderPort,
			"replica_ids":   partition.ReplicaIDs,
			"isr_ids":       partition.ISRIDs,
			"replica_count": len(partition.ReplicaIDs),
			"isr_count":     len(partition.ISRIDs),
		})
	}

	return app.ToolRunResult{
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "kafka topic metadata completed",
		}},
		Output: map[string]any{
			kafkaKeyProfileID: profile.ID,
			"topic":           topic,
			"partition_count": len(outputPartitions),
			"partitions":      outputPartitions,
		},
	}, nil
}

func ensureKafkaClient(client kafkaClient) kafkaClient {
	if client != nil {
		return client
	}
	return &liveKafkaClient{}
}

func (c *liveKafkaClient) Consume(ctx context.Context, req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:   req.Brokers,
		Topic:     req.Topic,
		Partition: req.Partition,
		MinBytes:  1,
		MaxBytes:  1 << 20,
	})
	defer reader.Close()

	setOffsetCtx, cancelSetOffset := context.WithTimeout(ctx, req.Timeout)
	defer cancelSetOffset()

	switch req.OffsetMode {
	case kafkaKeyTimestamp:
		if req.OffsetAt == nil {
			return nil, fmt.Errorf("timestamp offset requires timestamp value")
		}
		if err := reader.SetOffsetAt(setOffsetCtx, req.OffsetAt.UTC()); err != nil {
			return nil, err
		}
	default:
		if err := reader.SetOffset(req.OffsetStart); err != nil {
			return nil, err
		}
	}

	consumeCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	out := make([]kafkaConsumedMessage, 0, req.MaxMessages)
	for len(out) < req.MaxMessages {
		msg, err := reader.ReadMessage(consumeCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				break
			}
			return nil, err
		}
		out = append(out, kafkaConsumedMessage{
			Key:       msg.Key,
			Value:     msg.Value,
			Partition: msg.Partition,
			Offset:    msg.Offset,
			Time:      msg.Time,
		})
	}

	return out, nil
}

func (c *liveKafkaClient) Produce(ctx context.Context, req kafkaProduceRequest) error {
	if len(req.Brokers) == 0 {
		return fmt.Errorf("no kafka brokers configured")
	}

	dialCtx := ctx
	cancelDial := func() { /* no-op; replaced below if timeout is set */ }
	if req.Timeout > 0 {
		dialCtx, cancelDial = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancelDial()

	conn, err := kafkago.DialLeader(dialCtx, "tcp", req.Brokers[0], req.Topic, req.Partition)
	if err != nil {
		return err
	}
	defer conn.Close()
	if req.Timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(req.Timeout))
	}
	_, err = conn.WriteMessages(kafkago.Message{
		Key:   req.Key,
		Value: req.Value,
		Time:  time.Now().UTC(),
	})
	return err
}

func (c *liveKafkaClient) TopicMetadata(ctx context.Context, req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error) {
	conn, err := kafkago.DialContext(ctx, "tcp", req.Brokers[0])
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions(req.Topic)
	if err != nil {
		return nil, err
	}

	out := make([]kafkaTopicPartitionMetadata, 0, len(partitions))
	for _, partition := range partitions {
		if partition.Topic != req.Topic {
			continue
		}
		replicaIDs := make([]int, 0, len(partition.Replicas))
		for _, broker := range partition.Replicas {
			replicaIDs = append(replicaIDs, broker.ID)
		}
		isrIDs := make([]int, 0, len(partition.Isr))
		for _, broker := range partition.Isr {
			isrIDs = append(isrIDs, broker.ID)
		}
		out = append(out, kafkaTopicPartitionMetadata{
			PartitionID: partition.ID,
			LeaderHost:  partition.Leader.Host,
			LeaderPort:  partition.Leader.Port,
			ReplicaIDs:  replicaIDs,
			ISRIDs:      isrIDs,
		})
	}

	return out, nil
}

func resolveKafkaProfile(session domain.Session, requestedProfileID string) (connectionProfile, []string, *domain.Error) {
	profile, endpoint, err := resolveTypedProfile(session, requestedProfileID,
		[]string{"kafka"}, "dev.kafka",
		"localhost:9092")
	if err != nil {
		return connectionProfile{}, nil, err
	}
	brokers := splitKafkaBrokers(endpoint)
	if len(brokers) == 0 {
		return connectionProfile{}, nil, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "kafka profile endpoint not configured",
			Retryable: false,
		}
	}
	return profile, brokers, nil
}

func splitKafkaBrokers(raw string) []string {
	brokers := make([]string, 0, 2)
	for _, item := range strings.Split(raw, ",") {
		candidate := strings.TrimSpace(item)
		if candidate == "" {
			continue
		}
		candidate = strings.TrimPrefix(candidate, "kafka://")
		candidate = strings.TrimPrefix(candidate, "tcp://")
		brokers = append(brokers, candidate)
	}
	return brokers
}

func topicAllowedByProfile(topic string, profile connectionProfile) bool {
	raw, found := profile.Scopes["topics"]
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
		if topicPatternMatch(pattern, topic) {
			return true
		}
	}
	return false
}

type kafkaOffsetSpec struct {
	Mode        string
	OffsetStart int64
	OffsetAt    *time.Time
}

func resolveKafkaConsumeOffset(rawMode string, rawOffset any, timestampMS *int64) (kafkaOffsetSpec, error) {
	mode := strings.ToLower(strings.TrimSpace(rawMode))

	offsetModeFromLegacy, absoluteOffset, offsetProvided, parseErr := parseKafkaOffsetInput(rawOffset)
	if parseErr != nil {
		return kafkaOffsetSpec{}, parseErr
	}

	if mode == "" {
		switch {
		case timestampMS != nil:
			mode = kafkaKeyTimestamp
		case offsetModeFromLegacy != "":
			mode = offsetModeFromLegacy
		case offsetProvided:
			mode = kafkaOffsetAbsolute
		default:
			mode = kafkaOffsetLatest
		}
	}

	switch mode {
	case kafkaOffsetLatest:
		return resolveLatestOffset(offsetModeFromLegacy, absoluteOffset)
	case kafkaOffsetEarliest:
		return resolveEarliestOffset(offsetModeFromLegacy, absoluteOffset)
	case kafkaOffsetAbsolute:
		return resolveAbsoluteOffset(offsetModeFromLegacy, absoluteOffset)
	case kafkaKeyTimestamp:
		return resolveTimestampOffset(timestampMS, offsetProvided)
	default:
		return kafkaOffsetSpec{}, fmt.Errorf("offset_mode must be one of earliest, latest, absolute, timestamp")
	}
}

func resolveLatestOffset(legacy string, abs *int64) (kafkaOffsetSpec, error) {
	if legacy != "" && legacy != kafkaOffsetLatest {
		return kafkaOffsetSpec{}, fmt.Errorf("offset conflicts with offset_mode")
	}
	if abs != nil {
		return kafkaOffsetSpec{}, fmt.Errorf("numeric offset requires offset_mode=absolute")
	}
	return kafkaOffsetSpec{Mode: kafkaOffsetLatest, OffsetStart: kafkago.LastOffset}, nil
}

func resolveEarliestOffset(legacy string, abs *int64) (kafkaOffsetSpec, error) {
	if legacy != "" && legacy != kafkaOffsetEarliest {
		return kafkaOffsetSpec{}, fmt.Errorf("offset conflicts with offset_mode")
	}
	if abs != nil {
		return kafkaOffsetSpec{}, fmt.Errorf("numeric offset requires offset_mode=absolute")
	}
	return kafkaOffsetSpec{Mode: kafkaOffsetEarliest, OffsetStart: kafkago.FirstOffset}, nil
}

func resolveAbsoluteOffset(legacy string, abs *int64) (kafkaOffsetSpec, error) {
	if legacy != "" {
		return kafkaOffsetSpec{}, fmt.Errorf("offset must be a non-negative integer when offset_mode is absolute")
	}
	if abs == nil {
		return kafkaOffsetSpec{}, fmt.Errorf("offset is required when offset_mode is absolute")
	}
	return kafkaOffsetSpec{Mode: kafkaOffsetAbsolute, OffsetStart: *abs}, nil
}

func resolveTimestampOffset(tsMS *int64, offsetProvided bool) (kafkaOffsetSpec, error) {
	if tsMS == nil {
		return kafkaOffsetSpec{}, fmt.Errorf("timestamp_ms is required when offset_mode is timestamp")
	}
	if *tsMS < 0 {
		return kafkaOffsetSpec{}, fmt.Errorf("timestamp_ms must be >= 0")
	}
	if offsetProvided {
		return kafkaOffsetSpec{}, fmt.Errorf("offset must not be set when offset_mode is timestamp")
	}
	offsetAt := time.UnixMilli(*tsMS).UTC()
	return kafkaOffsetSpec{Mode: kafkaKeyTimestamp, OffsetAt: &offsetAt}, nil
}

func parseKafkaOffsetInput(rawOffset any) (legacyMode string, absoluteOffset *int64, provided bool, err error) {
	if rawOffset == nil {
		return "", nil, false, nil
	}

	switch typed := rawOffset.(type) {
	case string:
		value := strings.ToLower(strings.TrimSpace(typed))
		if value == "" {
			return "", nil, false, nil
		}
		switch value {
		case kafkaOffsetLatest, kafkaOffsetEarliest:
			return value, nil, true, nil
		default:
			parsed, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || parsed < 0 {
				return "", nil, true, fmt.Errorf(errKafkaOffsetInvalid)
			}
			return "", &parsed, true, nil
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || math.Trunc(typed) != typed || typed > float64(math.MaxInt64) {
			return "", nil, true, fmt.Errorf(errKafkaOffsetInvalid)
		}
		parsed := int64(typed)
		return "", &parsed, true, nil
	default:
		return "", nil, true, fmt.Errorf(errKafkaOffsetInvalid)
	}
}

func topicPatternMatch(pattern, topic string) bool {
	pattern = strings.TrimSpace(pattern)
	topic = strings.TrimSpace(topic)
	if pattern == "" || topic == "" {
		return false
	}
	if pattern == "*" || pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, ".>") {
		return strings.HasPrefix(topic, strings.TrimSuffix(pattern, ">"))
	}
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			return strings.HasPrefix(topic, parts[0]) && strings.HasSuffix(topic, parts[1])
		}
	}
	if strings.HasSuffix(pattern, ".") || strings.HasSuffix(pattern, ":") || strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(topic, pattern)
	}
	return false
}

