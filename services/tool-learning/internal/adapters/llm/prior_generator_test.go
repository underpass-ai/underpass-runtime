package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func TestPriorGenerator_ParsesLLMResponse(t *testing.T) {
	// Mock LLM server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `[
							{"tool_id": "fs.write_file", "estimated_p": 0.95, "rationale": "fundamental file op"},
							{"tool_id": "git.push", "estimated_p": 0.70, "rationale": "can fail on auth issues"},
							{"tool_id": "k8s.apply", "estimated_p": 0.40, "rationale": "high risk, often denied"}
						]`,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	gen := NewPriorGenerator(PriorGeneratorConfig{
		Endpoint: server.URL,
		Model:    "test-model",
	})

	tools := []domain.ToolDescription{
		{ID: "fs.write_file", Description: "Write a file", Risk: "low"},
		{ID: "git.push", Description: "Push to remote", Risk: "medium"},
		{ID: "k8s.apply", Description: "Apply manifest", Risk: "high"},
	}
	ctx := domain.ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"}

	priors, err := gen.GeneratePriors(context.Background(), tools, ctx)
	if err != nil {
		t.Fatalf("GeneratePriors: %v", err)
	}

	if len(priors) != 3 {
		t.Fatalf("expected 3 priors, got %d", len(priors))
	}

	// fs.write_file: p=0.95, n=10 → Alpha=9.5, Beta=0.5
	fw := priors["fs.write_file"]
	if fw.Alpha < 9.0 || fw.Alpha > 10.0 {
		t.Errorf("fs.write_file Alpha = %f, want ~9.5", fw.Alpha)
	}
	if fw.Rationale != "fundamental file op" {
		t.Errorf("rationale = %q", fw.Rationale)
	}

	// k8s.apply: p=0.40, n=10 → Alpha=4.0, Beta=6.0
	ka := priors["k8s.apply"]
	if ka.Alpha < 3.5 || ka.Alpha > 4.5 {
		t.Errorf("k8s.apply Alpha = %f, want ~4.0", ka.Alpha)
	}
}

func TestPriorGenerator_HandlesThinkingMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content":   nil,
						"reasoning": `<think>Let me analyze...</think>[{"tool_id": "fs.list", "estimated_p": 0.99, "rationale": "always works"}]`,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	gen := NewPriorGenerator(PriorGeneratorConfig{
		Endpoint: server.URL,
		Model:    "qwen3",
	})

	priors, err := gen.GeneratePriors(context.Background(),
		[]domain.ToolDescription{{ID: "fs.list"}},
		domain.ContextSignature{TaskFamily: "gen", Lang: "go"})
	if err != nil {
		t.Fatalf("GeneratePriors with thinking: %v", err)
	}
	if len(priors) != 1 {
		t.Fatalf("expected 1 prior, got %d", len(priors))
	}
	if priors["fs.list"].EstimatedP < 0.98 {
		t.Errorf("estimated_p = %f, want ~0.99", priors["fs.list"].EstimatedP)
	}
}

func TestPriorGenerator_HandlesLLMError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model overloaded"))
	}))
	defer server.Close()

	gen := NewPriorGenerator(PriorGeneratorConfig{
		Endpoint: server.URL,
		Model:    "test",
	})

	_, err := gen.GeneratePriors(context.Background(), nil, domain.ContextSignature{})
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
}

func TestParseEstimates_InvalidJSON(t *testing.T) {
	_, err := parseEstimates("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseEstimates_ValidJSON(t *testing.T) {
	input := `Here are the estimates: [{"tool_id":"x","estimated_p":0.5}] done.`
	estimates, err := parseEstimates(input)
	if err != nil {
		t.Fatalf("parseEstimates: %v", err)
	}
	if len(estimates) != 1 || estimates[0].ToolID != "x" {
		t.Errorf("unexpected result: %v", estimates)
	}
}
