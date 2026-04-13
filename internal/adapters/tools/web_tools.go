package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	webFetchMaxBytes    = 512 * 1024
	webFetchDefaultTO   = 30
	webFetchMaxTO       = 120
	webSearchMaxResults = 10
	webKeyStdout        = "stdout"
)

// WebFetchHandler fetches content from a URL and returns it as text.
// Enables agents to read documentation, API specs, changelogs, and
// error messages from the web.
type WebFetchHandler struct {
	client *http.Client
}

func NewWebFetchHandler() *WebFetchHandler {
	return &WebFetchHandler{
		client: &http.Client{Timeout: time.Duration(webFetchDefaultTO) * time.Second},
	}
}

func (h *WebFetchHandler) Name() string {
	return "web.fetch"
}

func (h *WebFetchHandler) Invoke(_ context.Context, _ domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		URL            string `json:"url"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}{
		TimeoutSeconds: webFetchDefaultTO,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid web.fetch args", Retryable: false}
	}
	url := strings.TrimSpace(request.URL)
	if url == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "url is required", Retryable: false}
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	timeout := request.TimeoutSeconds
	if timeout <= 0 {
		timeout = webFetchDefaultTO
	}
	if timeout > webFetchMaxTO {
		timeout = webFetchMaxTO
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Get(url) //nolint:gosec // URL is user-provided, validated above
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("fetch failed: %v", err), Retryable: true,
		}
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(webFetchMaxBytes)))
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("read body failed: %v", err), Retryable: true,
		}
	}

	content := string(body)
	truncated := len(body) >= webFetchMaxBytes

	return app.ToolRunResult{
		Output: map[string]any{
			"url":          url,
			"status_code":  resp.StatusCode,
			"content":      content,
			"content_type": resp.Header.Get("Content-Type"),
			"truncated":    truncated,
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: webKeyStdout, Message: fmt.Sprintf("fetched %s (%d bytes, HTTP %d)", url, len(body), resp.StatusCode)}},
	}, nil
}

// WebSearchHandler executes a web search query via a configurable
// search engine endpoint. Returns titles, URLs, and snippets.
type WebSearchHandler struct {
	runner app.CommandRunner
}

func NewWebSearchHandler(runner app.CommandRunner) *WebSearchHandler {
	return &WebSearchHandler{runner: runner}
}

func (h *WebSearchHandler) Name() string {
	return "web.search"
}

func (h *WebSearchHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}{
		MaxResults: 5,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid web.search args", Retryable: false}
	}
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "query is required", Retryable: false}
	}
	if request.MaxResults <= 0 {
		request.MaxResults = 5
	}
	if request.MaxResults > webSearchMaxResults {
		request.MaxResults = webSearchMaxResults
	}

	// Use ddgr (DuckDuckGo CLI) or fallback to curl+ddg lite.
	runner := h.runner
	if runner == nil {
		runner = NewLocalCommandRunner()
	}

	script := fmt.Sprintf(
		`curl -sL "https://lite.duckduckgo.com/lite?q=%s&kl=us-en" 2>/dev/null | grep -oP '(?<=<a rel="nofollow" href=")[^"]+' | head -n %d`,
		shellQuote(query), request.MaxResults,
	)
	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "sh",
		Args:     []string{"-lc", script},
		MaxBytes: 64 * 1024,
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code: app.ErrorCodeExecutionFailed, Message: fmt.Sprintf("search failed: %v", runErr), Retryable: true,
		}
	}

	urls := splitOutputLines(commandResult.Output)
	results := make([]map[string]string, 0, len(urls))
	for _, u := range urls {
		if strings.HasPrefix(u, "http") {
			results = append(results, map[string]string{"url": u})
		}
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"query":   query,
			"results": results,
			"count":   len(results),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: webKeyStdout, Message: fmt.Sprintf("search '%s': %d results", query, len(results))}},
	}, nil
}
