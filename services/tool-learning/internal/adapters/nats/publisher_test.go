package nats

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	gonats "github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func startTestNATS(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{
		Host: "127.0.0.1",
		Port: -1, // random port
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv
}

func TestPublishPolicyUpdated(t *testing.T) {
	srv := startTestNATS(t)

	conn, err := gonats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer conn.Close()

	// Subscribe before publishing — use atomic.Value to avoid data race.
	var received atomic.Value
	sub, err := conn.Subscribe(subjectPolicyUpdated, func(msg *gonats.Msg) {
		received.Store(msg.Data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	pub := NewPublisher(conn, "hourly")

	policies := []domain.ToolPolicy{
		{ContextSignature: "gen:go:std", ToolID: "fs.write", Alpha: 91, Beta: 11},
		{ContextSignature: "gen:go:std", ToolID: "fs.read", Alpha: 50, Beta: 1},
	}

	if err := pub.PublishPolicyUpdated(context.Background(), policies); err != nil {
		t.Fatalf("PublishPolicyUpdated: %v", err)
	}

	conn.Flush()
	time.Sleep(50 * time.Millisecond)

	raw := received.Load()
	if raw == nil {
		t.Fatal("no message received")
	}

	var event policyUpdatedEvent
	if err := json.Unmarshal(raw.([]byte), &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	if event.Event != subjectPolicyUpdated {
		t.Errorf("event = %q, want %q", event.Event, subjectPolicyUpdated)
	}
	if event.Schedule != "hourly" {
		t.Errorf("schedule = %q, want hourly", event.Schedule)
	}
	if event.PoliciesWritten != 2 {
		t.Errorf("policies_written = %d, want 2", event.PoliciesWritten)
	}
}

func TestNewPublisherFromURL(t *testing.T) {
	srv := startTestNATS(t)

	pub, conn, err := NewPublisherFromURL(srv.ClientURL(), "daily")
	if err != nil {
		t.Fatalf("NewPublisherFromURL: %v", err)
	}
	defer conn.Close()

	if pub.schedule != "daily" {
		t.Errorf("schedule = %q, want daily", pub.schedule)
	}
}
