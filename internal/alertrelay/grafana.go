package alertrelay

// GrafanaWebhookPayload is the structure Grafana sends to webhook contact points.
// See https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/webhook-notifier/
type GrafanaWebhookPayload struct {
	Receiver     string            `json:"receiver"`
	Status       string            `json:"status"` // firing, resolved
	Alerts       []GrafanaAlert    `json:"alerts"`
	GroupLabels  map[string]string `json:"groupLabels"`
	CommonLabels map[string]string `json:"commonLabels"`
	ExternalURL  string            `json:"externalURL"`
}

// GrafanaAlert is a single alert within the webhook payload.
type GrafanaAlert struct {
	Status       string             `json:"status"` // firing, resolved
	Labels       map[string]string  `json:"labels"`
	Annotations  map[string]string  `json:"annotations"`
	StartsAt     string             `json:"startsAt"`
	EndsAt       string             `json:"endsAt"`
	GeneratorURL string             `json:"generatorURL"`
	Fingerprint  string             `json:"fingerprint"`
	Values       map[string]float64 `json:"values"`
	DashboardURL string             `json:"dashboardURL"`
	PanelURL     string             `json:"panelURL"`
}

// ToAlertEvents converts a Grafana webhook payload into domain AlertEvents.
func (p GrafanaWebhookPayload) ToAlertEvents(idGen func() string) []AlertEvent {
	events := make([]AlertEvent, 0, len(p.Alerts))
	for _, a := range p.Alerts {
		eventType := EventAlertFired
		if a.Status == "resolved" {
			eventType = EventAlertResolved
		}

		severity := a.Labels["severity"]
		if severity == "" {
			severity = "warning"
		}

		events = append(events, AlertEvent{
			ID:           idGen(),
			Type:         eventType,
			Version:      EventVersion,
			Timestamp:    parseTimeOrNow(a.StartsAt),
			AlertName:    a.Labels["alertname"],
			Status:       a.Status,
			Severity:     severity,
			Summary:      a.Annotations["summary"],
			Description:  a.Annotations["description"],
			Source:       "grafana",
			Labels:       a.Labels,
			Values:       a.Values,
			DashboardURL: a.DashboardURL,
		})
	}
	return events
}
