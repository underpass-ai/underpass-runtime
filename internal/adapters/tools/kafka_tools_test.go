package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testKafkaTopic      = "sandbox.events"
	testKafkaDeadBroker = "127.0.0.1:1"
)

type fakeKafkaClient struct {
	consume       func(req kafkaConsumeRequest) ([]kafkaConsumedMessage, error)
	produce       func(req kafkaProduceRequest) error
	topicMetadata func(req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error)
}

func (f *fakeKafkaClient) Consume(_ context.Context, req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
	if f.consume != nil {
		return f.consume(req)
	}
	return []kafkaConsumedMessage{}, nil
}

func (f *fakeKafkaClient) Produce(_ context.Context, req kafkaProduceRequest) error {
	if f.produce != nil {
		return f.produce(req)
	}
	return nil
}

func (f *fakeKafkaClient) TopicMetadata(_ context.Context, req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error) {
	if f.topicMetadata != nil {
		return f.topicMetadata(req)
	}
	return []kafkaTopicPartitionMetadata{}, nil
}

func TestKafkaConsumeHandler_Success(t *testing.T) {
	handler := NewKafkaConsumeHandler(&fakeKafkaClient{
		consume: func(req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
			if len(req.Brokers) == 0 || req.Topic != testKafkaTopic || req.Timeout <= 0 || req.OffsetMode != "latest" || req.OffsetStart != kafkago.LastOffset {
				t.Fatalf("unexpected consume request: %#v", req)
			}
			return []kafkaConsumedMessage{
				{Key: []byte("k1"), Value: []byte("v1"), Partition: 0, Offset: 10, Time: time.Unix(1700000000, 0)},
			}, nil
		},
	})

	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events","max_messages":1}`))
	if err != nil {
		t.Fatalf("unexpected kafka.consume error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf(testErrMsgMapOutput, result.Output)
	}
	if output["profile_id"] != "dev.kafka" {
		t.Fatalf("unexpected profile_id: %#v", output["profile_id"])
	}
	if output["topic"] != testKafkaTopic {
		t.Fatalf("unexpected topic: %#v", output["topic"])
	}
	if output["offset_mode"] != "latest" {
		t.Fatalf("unexpected offset_mode: %#v", output["offset_mode"])
	}
}

func TestKafkaConsumeHandler_DeniesTopicOutsideProfileScopes(t *testing.T) {
	handler := NewKafkaConsumeHandler(&fakeKafkaClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"prod.events"}`))
	if err == nil {
		t.Fatal("expected topic policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf(testErrMsgUnexpectedCode, err.Code)
	}
}

func TestKafkaProduceHandler_Success(t *testing.T) {
	handler := NewKafkaProduceHandler(&fakeKafkaClient{
		produce: func(req kafkaProduceRequest) error {
			if len(req.Brokers) == 0 || req.Topic != testKafkaTopic || req.Partition != 0 || req.Timeout <= 0 {
				t.Fatalf("unexpected produce request: %#v", req)
			}
			if string(req.Key) != "k1" || string(req.Value) != "hello" {
				t.Fatalf("unexpected produce payload: key=%q value=%q", string(req.Key), string(req.Value))
			}
			return nil
		},
	})

	result, err := handler.Invoke(
		context.Background(),
		writableKafkaSession(),
		json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events","partition":0,"key":"k1","value":"hello"}`),
	)
	if err != nil {
		t.Fatalf("unexpected kafka.produce error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf(testErrMsgMapOutput, result.Output)
	}
	if output["produced"] != true {
		t.Fatalf("expected produced=true, got %#v", output["produced"])
	}
}

func TestKafkaProduceHandler_DeniesReadOnlyProfile(t *testing.T) {
	handler := NewKafkaProduceHandler(&fakeKafkaClient{})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events","value":"hello"}`))
	if err == nil {
		t.Fatal("expected read_only policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf(testErrMsgUnexpectedCode, err.Code)
	}
	if err.Message != "profile is read_only" {
		t.Fatalf("unexpected error message: %q", err.Message)
	}
}

func TestKafkaProduceHandler_ExecutionError(t *testing.T) {
	handler := NewKafkaProduceHandler(&fakeKafkaClient{
		produce: func(req kafkaProduceRequest) error {
			return errors.New("dial failed")
		},
	})

	_, err := handler.Invoke(context.Background(), writableKafkaSession(), json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events","value":"hello"}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testErrMsgUnexpectedCode, err.Code)
	}
}

func TestKafkaTopicMetadataHandler_Success(t *testing.T) {
	handler := NewKafkaTopicMetadataHandler(&fakeKafkaClient{
		topicMetadata: func(req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error) {
			if req.Topic != testKafkaTopic {
				t.Fatalf("unexpected topic: %s", req.Topic)
			}
			return []kafkaTopicPartitionMetadata{
				{PartitionID: 0, LeaderHost: "broker-a", LeaderPort: 9092, ReplicaIDs: []int{1, 2}, ISRIDs: []int{1}},
			}, nil
		},
	})

	session := domain.Session{
		Metadata: map[string]string{},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events"}`))
	if err != nil {
		t.Fatalf("unexpected kafka.topic_metadata error: %#v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf(testErrMsgMapOutput, result.Output)
	}
	if output["partition_count"] != 1 {
		t.Fatalf("unexpected partition_count: %#v", output["partition_count"])
	}
}

func TestKafkaConsumeHandler_MapsExecutionErrors(t *testing.T) {
	handler := NewKafkaConsumeHandler(&fakeKafkaClient{
		consume: func(req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
			return nil, errors.New("dial failed")
		},
	})
	session := domain.Session{
		Metadata: map[string]string{},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events"}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf(testErrMsgUnexpectedCode, err.Code)
	}
}

func TestKafkaConsumeHandler_OffsetModes(t *testing.T) {
	session := domain.Session{Metadata: map[string]string{}}

	t.Run("absolute", func(t *testing.T) {
		handler := NewKafkaConsumeHandler(&fakeKafkaClient{
			consume: func(req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
				if req.OffsetMode != "absolute" || req.OffsetStart != 42 || req.OffsetAt != nil {
					t.Fatalf("unexpected absolute offset consume request: %#v", req)
				}
				return []kafkaConsumedMessage{}, nil
			},
		})
		_, err := handler.Invoke(
			context.Background(), session,
			json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.absolute","partition":0,"offset_mode":"absolute","offset":42,"max_messages":1}`),
		)
		if err != nil {
			t.Fatalf("unexpected absolute mode consume error: %#v", err)
		}
	})

	t.Run("timestamp", func(t *testing.T) {
		handler := NewKafkaConsumeHandler(&fakeKafkaClient{
			consume: func(req kafkaConsumeRequest) ([]kafkaConsumedMessage, error) {
				if req.OffsetMode != "timestamp" || req.OffsetAt == nil || req.OffsetStart != 0 {
					t.Fatalf("unexpected timestamp offset consume request: %#v", req)
				}
				expected := time.UnixMilli(1730000000000).UTC()
				if !req.OffsetAt.Equal(expected) {
					t.Fatalf("unexpected timestamp offset value: got=%s expected=%s", req.OffsetAt.UTC(), expected)
				}
				return []kafkaConsumedMessage{}, nil
			},
		})
		_, err := handler.Invoke(
			context.Background(), session,
			json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.timestamp","partition":0,"offset_mode":"timestamp","timestamp_ms":1730000000000,"max_messages":1}`),
		)
		if err != nil {
			t.Fatalf("unexpected timestamp mode consume error: %#v", err)
		}
	})
}

func TestKafkaHandlers_NamesAndLiveClientErrors(t *testing.T) {
	if NewKafkaConsumeHandler(nil).Name() != "kafka.consume" {
		t.Fatal("unexpected kafka.consume name")
	}
	if NewKafkaProduceHandler(nil).Name() != "kafka.produce" {
		t.Fatal("unexpected kafka.produce name")
	}
	if NewKafkaTopicMetadataHandler(nil).Name() != "kafka.topic_metadata" {
		t.Fatal("unexpected kafka.topic_metadata name")
	}

	client := &liveKafkaClient{}
	ctx := context.Background()
	messages, err := client.Consume(ctx, kafkaConsumeRequest{
		Brokers:     []string{testKafkaDeadBroker},
		Topic:       testKafkaTopic,
		Partition:   0,
		OffsetStart: 0,
		MaxMessages: 1,
		Timeout:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected live kafka consume error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected zero consumed messages from dead broker, got %d", len(messages))
	}

	_, err = client.TopicMetadata(ctx, kafkaTopicMetadataRequest{
		Brokers: []string{testKafkaDeadBroker},
		Topic:   testKafkaTopic,
	})
	if err == nil {
		t.Fatal("expected live kafka metadata connection error")
	}

	err = client.Produce(ctx, kafkaProduceRequest{
		Brokers:   []string{testKafkaDeadBroker},
		Topic:     testKafkaTopic,
		Partition: 0,
		Key:       []byte("k1"),
		Value:     []byte("v1"),
		Timeout:   5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected live kafka produce connection error")
	}
}

func TestKafkaHelpers_ProfileResolutionAndPatterning(t *testing.T) {
	t.Run("profile_resolution", func(t *testing.T) {
		_, _, err := resolveKafkaProfile(domain.Session{}, "")
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected profile_id validation error, got %#v", err)
		}

		sessionWrongKind := domain.Session{
			Metadata: map[string]string{
				"connection_profiles_json": `[{"id":"x","kind":"nats","read_only":true,"scopes":{"topics":["sandbox."]}}]`,
			},
		}
		_, _, err = resolveKafkaProfile(sessionWrongKind, "x")
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected wrong kind error, got %#v", err)
		}
	})

	t.Run("split_brokers", func(t *testing.T) {
		brokers := splitKafkaBrokers("kafka://broker-a:9092, tcp://broker-b:9092, broker-c:9092")
		if len(brokers) != 3 || brokers[0] != "broker-a:9092" || brokers[1] != "broker-b:9092" || brokers[2] != "broker-c:9092" {
			t.Fatalf("unexpected splitKafkaBrokers output: %#v", brokers)
		}
	})

	t.Run("topic_allowed_by_profile", func(t *testing.T) {
		profile := connectionProfile{Scopes: map[string]any{"topics": []any{"sandbox.", "dev.>"}}}
		if !topicAllowedByProfile("sandbox.jobs", profile) {
			t.Fatal("expected topicAllowedByProfile allow")
		}
		if topicAllowedByProfile("prod.jobs", profile) {
			t.Fatal("expected topicAllowedByProfile deny")
		}
	})

	t.Run("resolve_consume_offset", func(t *testing.T) {
		offsetSpec, offsetErr := resolveKafkaConsumeOffset("", "earliest", nil)
		if offsetErr != nil || offsetSpec.OffsetStart != kafkago.FirstOffset || offsetSpec.Mode != "earliest" {
			t.Fatalf("unexpected earliest offset parse: offset=%+v err=%v", offsetSpec, offsetErr)
		}
		offsetSpec, offsetErr = resolveKafkaConsumeOffset("", float64(77), nil)
		if offsetErr != nil || offsetSpec.Mode != "absolute" || offsetSpec.OffsetStart != 77 {
			t.Fatalf("unexpected absolute offset parse: offset=%+v err=%v", offsetSpec, offsetErr)
		}
		timestamp := int64(1730000000000)
		offsetSpec, offsetErr = resolveKafkaConsumeOffset("timestamp", nil, &timestamp)
		if offsetErr != nil || offsetSpec.Mode != "timestamp" || offsetSpec.OffsetAt == nil || !offsetSpec.OffsetAt.Equal(time.UnixMilli(timestamp).UTC()) {
			t.Fatalf("unexpected timestamp offset parse: offset=%+v err=%v", offsetSpec, offsetErr)
		}
		if _, offsetErr = resolveKafkaConsumeOffset("middle", nil, nil); offsetErr == nil {
			t.Fatal("expected resolveKafkaConsumeOffset validation error")
		}
		if _, offsetErr = resolveKafkaConsumeOffset("absolute", nil, nil); offsetErr == nil {
			t.Fatal("expected absolute offset missing value error")
		}
		if _, offsetErr = resolveKafkaConsumeOffset("timestamp", nil, nil); offsetErr == nil {
			t.Fatal("expected timestamp offset missing value error")
		}
	})

	t.Run("topic_pattern_match", func(t *testing.T) {
		if !topicPatternMatch("sandbox.>", "sandbox.jobs.created") {
			t.Fatal("expected topicPatternMatch with .> wildcard")
		}
		if !topicPatternMatch("sandbox.*.created", "sandbox.jobs.created") {
			t.Fatal("expected topicPatternMatch with * wildcard")
		}
		if topicPatternMatch("sandbox.", "prod.jobs") {
			t.Fatal("did not expect topicPatternMatch for disallowed topic")
		}
	})
}

func TestParseKafkaOffsetInput_AllBranches(t *testing.T) {
	t.Run("nil_input", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput(nil)
		if err != nil || mode != "" || absOff != nil || provided {
			t.Fatalf("expected nil result for nil input, got mode=%q provided=%v err=%v", mode, provided, err)
		}
	})
	t.Run("empty_string", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput("")
		if err != nil || mode != "" || absOff != nil || provided {
			t.Fatalf("expected empty result for empty string, got mode=%q provided=%v err=%v", mode, provided, err)
		}
	})
	t.Run("latest", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput("latest")
		if err != nil || mode != "latest" || absOff != nil || !provided {
			t.Fatalf("unexpected latest result: mode=%q provided=%v err=%v", mode, provided, err)
		}
	})
	t.Run("earliest", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput("earliest")
		if err != nil || mode != "earliest" || absOff != nil || !provided {
			t.Fatalf("unexpected earliest result: mode=%q provided=%v err=%v", mode, provided, err)
		}
	})
	t.Run("numeric_string_100", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput("100")
		if err != nil || mode != "" || absOff == nil || *absOff != 100 || !provided {
			t.Fatalf("unexpected numeric string result: mode=%q off=%v provided=%v err=%v", mode, absOff, provided, err)
		}
	})
	t.Run("negative_numeric_string", func(t *testing.T) {
		_, _, _, err := parseKafkaOffsetInput("-5")
		if err == nil {
			t.Fatal("expected error for negative numeric string")
		}
	})
	t.Run("float64_42", func(t *testing.T) {
		mode, absOff, provided, err := parseKafkaOffsetInput(float64(42))
		if err != nil || mode != "" || absOff == nil || *absOff != 42 || !provided {
			t.Fatalf("unexpected float64 result: mode=%q off=%v provided=%v err=%v", mode, absOff, provided, err)
		}
	})
	t.Run("non_integer_float64", func(t *testing.T) {
		_, _, _, err := parseKafkaOffsetInput(float64(1.5))
		if err == nil {
			t.Fatal("expected error for non-integer float64")
		}
	})
	t.Run("negative_float64", func(t *testing.T) {
		_, _, _, err := parseKafkaOffsetInput(float64(-1))
		if err == nil {
			t.Fatal("expected error for negative float64")
		}
	})
	t.Run("unsupported_type", func(t *testing.T) {
		_, _, _, err := parseKafkaOffsetInput(true)
		if err == nil {
			t.Fatal("expected error for unsupported offset type")
		}
	})
}

func TestKafkaTopicMetadataHandler_ErrorPaths(t *testing.T) {
	handler := NewKafkaTopicMetadataHandler(&fakeKafkaClient{})
	session := domain.Session{Metadata: map[string]string{}}

	// empty topic
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":""}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty topic, got %#v", err)
	}

	// profile not found (unknown profile_id)
	_, err = handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"unknown","topic":"sandbox.events"}`))
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found for unknown profile, got %#v", err)
	}

	// topic outside profile allowlist
	_, err = handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"prod.forbidden"}`))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy_denied for out-of-scope topic, got %#v", err)
	}

	// client error
	handlerClientErr := NewKafkaTopicMetadataHandler(&fakeKafkaClient{
		topicMetadata: func(req kafkaTopicMetadataRequest) ([]kafkaTopicPartitionMetadata, error) {
			return nil, errors.New("broker unavailable")
		},
	})
	_, err = handlerClientErr.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.kafka","topic":"sandbox.events"}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed from client error, got %#v", err)
	}
}

// ---------------------------------------------------------------------------
// kafkaTrimMessageBytes — all branches
// ---------------------------------------------------------------------------

func TestKafkaTrimMessageBytes(t *testing.T) {
	t.Run("no_trimming_needed", func(t *testing.T) {
		key := []byte("key")
		value := []byte("value")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 100)
		if anyTrimmed || keyTrimmed || valTrimmed {
			t.Fatal("expected no trimming")
		}
		if string(tKey) != "key" || string(tVal) != "value" {
			t.Fatalf("unexpected result: key=%q value=%q", tKey, tVal)
		}
	})
	t.Run("exact_fit", func(t *testing.T) {
		key := []byte("ab")
		value := []byte("cd")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 4)
		if anyTrimmed || keyTrimmed || valTrimmed {
			t.Fatal("expected no trimming when exact fit")
		}
		if string(tKey) != "ab" || string(tVal) != "cd" {
			t.Fatalf("unexpected result: key=%q value=%q", tKey, tVal)
		}
	})
	t.Run("key_exceeds_remaining", func(t *testing.T) {
		key := []byte("abcdef")
		value := []byte("xyz")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 3)
		if !anyTrimmed || !keyTrimmed || !valTrimmed {
			t.Fatalf("expected all trimmed: anyTrimmed=%v keyTrimmed=%v valTrimmed=%v", anyTrimmed, keyTrimmed, valTrimmed)
		}
		if string(tKey) != "abc" {
			t.Fatalf("expected key trimmed to 'abc', got %q", tKey)
		}
		if len(tVal) != 0 {
			t.Fatalf("expected empty value, got %q", tVal)
		}
	})
	t.Run("key_fits_value_trimmed", func(t *testing.T) {
		key := []byte("ab")
		value := []byte("cdefgh")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 5)
		if !anyTrimmed || keyTrimmed || !valTrimmed {
			t.Fatalf("expected only value trimmed: anyTrimmed=%v keyTrimmed=%v valTrimmed=%v", anyTrimmed, keyTrimmed, valTrimmed)
		}
		if string(tKey) != "ab" {
			t.Fatalf("expected key unchanged, got %q", tKey)
		}
		if string(tVal) != "cde" {
			t.Fatalf("expected value trimmed to 'cde', got %q", tVal)
		}
	})
	t.Run("key_equals_remaining_empty_value", func(t *testing.T) {
		key := []byte("abc")
		value := []byte("")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 3)
		if anyTrimmed || keyTrimmed || valTrimmed {
			t.Fatal("expected no trimming when key=remaining and value empty")
		}
		if string(tKey) != "abc" || len(tVal) != 0 {
			t.Fatalf("unexpected result: key=%q value=%q", tKey, tVal)
		}
	})
	t.Run("key_equals_remaining_with_value", func(t *testing.T) {
		key := []byte("abc")
		value := []byte("x")
		tKey, tVal, keyTrimmed, valTrimmed, anyTrimmed := kafkaTrimMessageBytes(key, value, 3)
		if !anyTrimmed || keyTrimmed || !valTrimmed {
			t.Fatalf("expected value trimmed only: anyTrimmed=%v keyTrimmed=%v valTrimmed=%v", anyTrimmed, keyTrimmed, valTrimmed)
		}
		if string(tKey) != "abc" {
			t.Fatalf("expected key unchanged, got %q", tKey)
		}
		if len(tVal) != 0 {
			t.Fatalf("expected empty value, got %q", tVal)
		}
	})
}

func writableKafkaSession() domain.Session {
	return domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"dev.kafka","kind":"kafka","read_only":false,"scopes":{"topics":["sandbox.>","dev.>"]}}]`,
		},
	}
}
