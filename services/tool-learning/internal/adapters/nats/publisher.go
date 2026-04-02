package nats

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

const (
	subjectRunStarted        = "tool_learning.run.started"
	subjectRunCompleted      = "tool_learning.run.completed"
	subjectRunFailed         = "tool_learning.run.failed"
	subjectPolicyComputed    = "tool_learning.policy.computed"
	subjectPolicyUpdated     = "tool_learning.policy.updated"
	subjectSnapshotPublished = "tool_learning.snapshot.published"
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
func NewPublisherFromURL(url, schedule string, tlsCfg *tls.Config) (*Publisher, *nats.Conn, error) {
	var opts []nats.Option
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
	}
	conn, err := nats.Connect(url, opts...)
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

// PublishRunStarted emits tool_learning.run.started.
func (p *Publisher) PublishRunStarted(_ context.Context, run domain.PolicyRun) error {
	return p.publishJSON(subjectRunStarted, map[string]any{
		"event":             subjectRunStarted,
		"run_id":            run.RunID,
		"schedule":          run.Schedule,
		"algorithm_id":      run.AlgorithmID,
		"algorithm_version": run.AlgorithmVersion,
		"window":            run.Window,
		"started_at":        run.StartedAt.Format(time.RFC3339),
	})
}

// PublishRunCompleted emits tool_learning.run.completed.
func (p *Publisher) PublishRunCompleted(_ context.Context, run domain.PolicyRun) error {
	return p.publishJSON(subjectRunCompleted, map[string]any{
		"event":             subjectRunCompleted,
		"run_id":            run.RunID,
		"aggregates_read":   run.AggregatesRead,
		"policies_written":  run.PoliciesWritten,
		"policies_filtered": run.PoliciesFiltered,
		"snapshot_ref":      run.SnapshotRef,
		"duration_ms":       run.DurationMs,
	})
}

// PublishRunFailed emits tool_learning.run.failed.
func (p *Publisher) PublishRunFailed(_ context.Context, run domain.PolicyRun) error {
	return p.publishJSON(subjectRunFailed, map[string]any{
		"event":       subjectRunFailed,
		"run_id":      run.RunID,
		"error_code":  run.ErrorCode,
		"message":     run.ErrorMessage,
		"duration_ms": run.DurationMs,
	})
}

// PublishPolicyComputed emits tool_learning.policy.computed per batch.
func (p *Publisher) PublishPolicyComputed(_ context.Context, run domain.PolicyRun, policies []domain.ToolPolicy) error {
	for _, pol := range policies {
		if err := p.publishJSON(subjectPolicyComputed, map[string]any{
			"event":             subjectPolicyComputed,
			"policy_id":         pol.ValkeyKey(""),
			"run_id":            run.RunID,
			"context_signature": pol.ContextSignature,
			"tool_id":           pol.ToolID,
			"algorithm_id":      run.AlgorithmID,
			"algorithm_version": run.AlgorithmVersion,
			"confidence":        pol.Confidence,
			"snapshot_ref":      run.SnapshotRef,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PublishSnapshotPublished emits tool_learning.snapshot.published.
func (p *Publisher) PublishSnapshotPublished(_ context.Context, run domain.PolicyRun, snapshotRef string) error {
	return p.publishJSON(subjectSnapshotPublished, map[string]any{
		"event":        subjectSnapshotPublished,
		"run_id":       run.RunID,
		"snapshot_ref": snapshotRef,
	})
}

// publishJSON marshals payload and publishes to subject.
func (p *Publisher) publishJSON(subject string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.conn.Publish(subject, data)
}

// Close drains and closes the NATS connection.
func (p *Publisher) Close() {
	if p.conn != nil {
		_ = p.conn.Drain()
	}
}
