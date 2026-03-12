package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

const (
	subjectPolicyUpdated = "tool_learning.policy.updated"
)

// policyUpdatedEvent is the NATS event payload per contract v1.
type policyUpdatedEvent struct {
	Event            string `json:"event"`
	Ts               string `json:"ts"`
	Schedule         string `json:"schedule"`
	PoliciesWritten  int    `json:"policies_written"`
	PoliciesFiltered int    `json:"policies_filtered"`
}

// Publisher implements app.PolicyEventPublisher using NATS.
type Publisher struct {
	conn     *nats.Conn
	schedule string
}

// NewPublisher creates a NATS publisher.
func NewPublisher(conn *nats.Conn, schedule string) *Publisher {
	return &Publisher{conn: conn, schedule: schedule}
}

// NewPublisherFromURL connects to NATS and returns a publisher.
func NewPublisherFromURL(url, schedule string) (*Publisher, *nats.Conn, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, nil, fmt.Errorf("nats connect: %w", err)
	}
	return NewPublisher(conn, schedule), conn, nil
}

// PublishPolicyUpdated publishes a policy update summary event.
func (p *Publisher) PublishPolicyUpdated(_ context.Context, policies []domain.ToolPolicy, filtered int) error {
	event := policyUpdatedEvent{
		Event:            subjectPolicyUpdated,
		Ts:               time.Now().UTC().Format(time.RFC3339),
		Schedule:         p.schedule,
		PoliciesWritten:  len(policies),
		PoliciesFiltered: filtered,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	return p.conn.Publish(subjectPolicyUpdated, data)
}

// Close drains and closes the NATS connection.
func (p *Publisher) Close() {
	if p.conn != nil {
		_ = p.conn.Drain()
	}
}
