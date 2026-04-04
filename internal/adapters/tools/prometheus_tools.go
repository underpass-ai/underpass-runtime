package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ---------------------------------------------------------------------------
// prometheus.query
// ---------------------------------------------------------------------------

type PrometheusQueryHandler struct {
	httpClient *http.Client
}

func NewPrometheusQueryHandler() *PrometheusQueryHandler {
	return &PrometheusQueryHandler{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (h *PrometheusQueryHandler) Name() string { return "prometheus.query" }

func (h *PrometheusQueryHandler) Invoke(_ context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Query          string  `json:"query"`
		URL            string  `json:"url"`
		ExpectedBelow  float64 `json:"expected_below"`
		ExpectedAbove  float64 `json:"expected_above"`
		TimeoutSeconds int     `json:"timeout_seconds"`
	}{TimeoutSeconds: 30}
	if err := json.Unmarshal(args, &request); err != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid prometheus.query args", Retryable: false}
	}
	if strings.TrimSpace(request.Query) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "query is required", Retryable: false}
	}

	prometheusURL := request.URL
	if prometheusURL == "" {
		prometheusURL = session.Metadata["prometheus_url"]
	}
	if prometheusURL == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "prometheus URL not configured (set url arg or prometheus_url in session metadata)", Retryable: false}
	}

	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", strings.TrimRight(prometheusURL, "/"), url.QueryEscape(request.Query))
	deadline := time.Now().Add(time.Duration(request.TimeoutSeconds) * time.Second)

	for time.Now().Before(deadline) {
		value, err := h.queryPrometheus(queryURL)
		if err != nil {
			if request.TimeoutSeconds <= 0 {
				return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("prometheus query failed: %v", err), Retryable: true}
			}
			time.Sleep(5 * time.Second)
			continue
		}

		thresholdMet := true
		if request.ExpectedBelow > 0 && value >= request.ExpectedBelow {
			thresholdMet = false
		}
		if request.ExpectedAbove > 0 && value <= request.ExpectedAbove {
			thresholdMet = false
		}

		if thresholdMet {
			return app.ToolRunResult{
				Output:   map[string]any{"value": value, "threshold_met": true, "query": request.Query},
				ExitCode: 0,
			}, nil
		}

		if request.TimeoutSeconds <= 0 {
			break
		}
		time.Sleep(10 * time.Second)
	}

	return app.ToolRunResult{
		Output:   map[string]any{"threshold_met": false, "query": request.Query, "timeout": true},
		ExitCode: 1,
	}, nil
}

func (h *PrometheusQueryHandler) queryPrometheus(queryURL string) (float64, error) {
	resp, err := h.httpClient.Get(queryURL) //nolint:gosec // URL is constructed from validated input
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, err
	}

	var promResp struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &promResp); err != nil {
		return 0, fmt.Errorf("parse prometheus response: %w", err)
	}
	if promResp.Status != "success" || len(promResp.Data.Result) == 0 {
		return 0, fmt.Errorf("no results for query")
	}
	if len(promResp.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("unexpected value format")
	}

	var valueStr string
	if err := json.Unmarshal(promResp.Data.Result[0].Value[1], &valueStr); err != nil {
		return 0, fmt.Errorf("parse value: %w", err)
	}
	return strconv.ParseFloat(valueStr, 64)
}
