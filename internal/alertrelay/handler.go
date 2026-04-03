package alertrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Publisher sends alert events to a message bus.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

// Handler receives Grafana webhook POSTs and publishes NATS domain events.
type Handler struct {
	publisher     Publisher
	subjectPrefix string
	authToken     string
	logger        *slog.Logger
}

// HandlerConfig configures the webhook handler.
type HandlerConfig struct {
	Publisher     Publisher
	SubjectPrefix string // e.g., "observability.alert"
	AuthToken     string // shared secret for webhook auth (empty = no auth)
	Logger        *slog.Logger
}

// NewHandler creates a webhook handler.
func NewHandler(cfg HandlerConfig) *Handler {
	prefix := cfg.SubjectPrefix
	if prefix == "" {
		prefix = "observability.alert"
	}
	return &Handler{
		publisher:     cfg.Publisher,
		subjectPrefix: prefix,
		authToken:     cfg.AuthToken,
		logger:        cfg.Logger,
	}
}

// ServeHTTP handles the Grafana webhook POST.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.authToken != "" {
		token := r.Header.Get("Authorization")
		if token != "Bearer "+h.authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var payload GrafanaWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.logger.Warn("invalid webhook payload", "error", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	events := payload.ToAlertEvents(newEventID)
	published := 0
	for _, evt := range events {
		subject := h.subjectPrefix + "." + evt.Status
		data, err := json.Marshal(evt)
		if err != nil {
			h.logger.Warn("marshal alert event failed", "alert", evt.AlertName, "error", err)
			continue
		}
		if err := h.publisher.Publish(r.Context(), subject, data); err != nil {
			h.logger.Warn("publish alert event failed", "subject", subject, "alert", evt.AlertName, "error", err)
			continue
		}
		published++
		h.logger.Info("alert event published",
			"subject", subject,
			"alert", evt.AlertName,
			"severity", evt.Severity,
			"status", evt.Status,
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"received":  len(events),
		"published": published,
	})
}

func newEventID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "alert-" + hex.EncodeToString(b)
}

func parseTimeOrNow(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

// NATSPublisher implements Publisher using a NATS connection.
type NATSPublisher struct {
	conn NATSConn
}

// NATSConn is the minimal NATS interface needed.
type NATSConn interface {
	Publish(subject string, data []byte) error
}

// NewNATSPublisher creates a publisher backed by NATS.
func NewNATSPublisher(conn NATSConn) *NATSPublisher {
	return &NATSPublisher{conn: conn}
}

// Publish sends data to a NATS subject.
func (p *NATSPublisher) Publish(_ context.Context, subject string, data []byte) error {
	if err := p.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("nats publish %s: %w", subject, err)
	}
	return nil
}
