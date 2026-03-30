// Package llm provides adapters for LLM-based prior generation.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

// PriorGenerator calls an OpenAI-compatible LLM API to generate
// informative Beta priors for tool selection.
type PriorGenerator struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
	cfg      domain.PriorConfig
}

// PriorGeneratorConfig holds configuration for the LLM prior generator.
type PriorGeneratorConfig struct {
	Endpoint    string            // e.g. "http://vllm:8000" or "https://api.openai.com"
	Model       string            // e.g. "Qwen/Qwen3-8B" or "gpt-4o-mini"
	APIKey      string            // bearer token (optional for vLLM)
	PriorConfig domain.PriorConfig
	Timeout     time.Duration     // default 120s
}

// NewPriorGenerator creates an LLM-backed prior generator.
func NewPriorGenerator(cfg PriorGeneratorConfig) *PriorGenerator {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	pcfg := cfg.PriorConfig
	if pcfg.EquivalentN == 0 {
		pcfg = domain.DefaultPriorConfig()
	}
	return &PriorGenerator{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		client:   &http.Client{Timeout: timeout},
		cfg:      pcfg,
	}
}

// llmResponse is the expected JSON from the LLM.
type llmEstimate struct {
	ToolID     string  `json:"tool_id"`
	EstimatedP float64 `json:"estimated_p"`
	Rationale  string  `json:"rationale"`
}

// GeneratePriors calls the LLM to estimate success probabilities for each
// tool in the given context. Returns a PriorMap ready for the sampler.
func (g *PriorGenerator) GeneratePriors(
	ctx context.Context,
	tools []domain.ToolDescription,
	context domain.ContextSignature,
) (domain.PriorMap, error) {
	prompt := domain.LLMPriorPrompt(tools, context)

	content, err := g.chat(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}

	estimates, err := parseEstimates(content)
	if err != nil {
		return nil, fmt.Errorf("parse estimates: %w", err)
	}

	priors := make(domain.PriorMap, len(estimates))
	for _, est := range estimates {
		prior := domain.ComputePrior(est.ToolID, est.EstimatedP, g.cfg)
		prior.Rationale = est.Rationale
		priors[est.ToolID] = prior
	}
	return priors, nil
}

func (g *PriorGenerator) chat(ctx context.Context, prompt string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": g.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature":          0.1,
		"max_tokens":           4096,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST",
		g.endpoint+"/v1/chat/completions",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(data.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	content := data.Choices[0].Message.Content
	if content == "" {
		content = data.Choices[0].Message.Reasoning
	}
	// Strip thinking tags.
	if idx := strings.Index(content, "</think>"); idx >= 0 {
		content = strings.TrimSpace(content[idx+len("</think>"):])
	}
	return content, nil
}

func parseEstimates(content string) ([]llmEstimate, error) {
	// Find JSON array in response.
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found in response: %s", truncate(content, 200))
	}

	var estimates []llmEstimate
	if err := json.Unmarshal([]byte(content[start:end+1]), &estimates); err != nil {
		return nil, fmt.Errorf("unmarshal estimates: %w: %s", err, truncate(content[start:end+1], 200))
	}
	return estimates, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
