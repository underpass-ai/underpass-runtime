package alertrelay

import (
	"encoding/json"
	"time"
)

// Domain event types for observability alerts.
const (
	EventAlertFired    = "observability.alert.fired"
	EventAlertResolved = "observability.alert.resolved"
	EventVersion       = "v1"
)

// AlertEvent is the NATS domain event emitted when Grafana fires or resolves an alert.
// Agents subscribe to these events to trigger expert remediation workflows.
type AlertEvent struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Version      string             `json:"version"`
	Timestamp    time.Time          `json:"timestamp"`
	AlertName    string             `json:"alert_name"`
	Status       string             `json:"status"` // firing, resolved
	Severity     string             `json:"severity"`
	Summary      string             `json:"summary"`
	Description  string             `json:"description,omitempty"`
	Source       string             `json:"source"` // grafana
	Labels       map[string]string  `json:"labels,omitempty"`
	Values       map[string]float64 `json:"values,omitempty"`
	DashboardURL string             `json:"dashboard_url,omitempty"`
	Payload      json.RawMessage    `json:"payload,omitempty"`
}
