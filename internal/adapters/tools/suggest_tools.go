package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	suggestDefaultTopK = 5
	suggestMaxTopK     = 20
	suggestKeyStdout   = "stdout"
)

// ToolSuggestHandler recommends tools for a given task description.
// Uses the embedded capability catalog to rank tools by relevance
// to the agent's intent. This is the runtime's core differentiator:
// agents can ask "what tool should I use?" and get ranked, explained
// suggestions backed by the full policy-governed catalog.
type ToolSuggestHandler struct{}

func NewToolSuggestHandler() *ToolSuggestHandler {
	return &ToolSuggestHandler{}
}

func (h *ToolSuggestHandler) Name() string {
	return "tool.suggest"
}

func (h *ToolSuggestHandler) Invoke(_ context.Context, _ domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Task   string `json:"task"`
		TopK   int    `json:"top_k"`
		Scope  string `json:"scope"`
		Family string `json:"family"`
	}{
		TopK: suggestDefaultTopK,
	}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid tool.suggest args", Retryable: false}
	}
	if strings.TrimSpace(request.Task) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "task description is required", Retryable: false}
	}
	if request.TopK <= 0 {
		request.TopK = suggestDefaultTopK
	}
	if request.TopK > suggestMaxTopK {
		request.TopK = suggestMaxTopK
	}

	caps := DefaultCapabilities()
	scored := suggestRankTools(caps, request.Task, request.Scope, request.Family)

	if len(scored) > request.TopK {
		scored = scored[:request.TopK]
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"task":        request.Task,
			"suggestions": scored,
			"count":       len(scored),
		},
		Logs: []domain.LogLine{{At: time.Now().UTC(), Channel: suggestKeyStdout, Message: fmt.Sprintf("suggested %d tools for: %s", len(scored), request.Task)}},
	}, nil
}

type toolSuggestion struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
	RiskLevel   string  `json:"risk_level"`
	Why         string  `json:"why"`
}

func suggestRankTools(caps []domain.Capability, task, scopeFilter, familyFilter string) []toolSuggestion {
	taskLower := strings.ToLower(task)
	words := strings.Fields(taskLower)

	var results []toolSuggestion
	for _, cap := range caps {
		if scopeFilter != "" && string(cap.Scope) != scopeFilter {
			continue
		}
		if familyFilter != "" && !strings.HasPrefix(cap.Name, familyFilter+".") {
			continue
		}

		score := suggestScore(cap, taskLower, words)
		if score <= 0 {
			continue
		}

		why := suggestExplain(cap, taskLower, words)
		results = append(results, toolSuggestion{
			Name:        cap.Name,
			Score:       score,
			Description: cap.Description,
			RiskLevel:   string(cap.RiskLevel),
			Why:         why,
		})
	}

	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results
}

func suggestScore(cap domain.Capability, taskLower string, words []string) float64 {
	score := 0.0
	nameLower := strings.ToLower(cap.Name)
	descLower := strings.ToLower(cap.Description)

	// Exact name match in task.
	if strings.Contains(taskLower, nameLower) {
		score += 1.0
	}

	// Word matches in name and description.
	for _, word := range words {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(nameLower, word) {
			score += 0.5
		}
		if strings.Contains(descLower, word) {
			score += 0.2
		}
	}

	// Family prefix match (e.g. task mentions "file" and tool is fs.*).
	familyBonus := map[string][]string{
		"fs.":        {"file", "read", "write", "edit", "search", "find", "directory", "folder", "create", "delete", "move", "copy", "insert", "line", "glob", "pattern"},
		"git.":       {"git", "commit", "push", "pull", "diff", "branch", "merge", "log", "status", "checkout", "blame"},
		"shell.":     {"run", "execute", "command", "make", "build", "install", "pip", "npm", "cargo", "curl"},
		"repo.":      {"project", "tree", "structure", "test", "build", "coverage", "symbol", "reference", "analyze"},
		"github.":    {"pr", "pull request", "issue", "merge", "ci", "check", "workflow"},
		"container.": {"container", "docker", "image", "pod"},
		"k8s.":       {"kubernetes", "k8s", "deploy", "pod", "service", "rollout"},
		"workspace.": {"undo", "revert", "rollback", "checkpoint"},
	}
	for prefix, keywords := range familyBonus {
		if !strings.HasPrefix(nameLower, prefix) {
			continue
		}
		for _, kw := range keywords {
			if strings.Contains(taskLower, kw) {
				score += 0.3
				break
			}
		}
	}

	// Penalize high-risk tools slightly.
	if cap.RiskLevel == domain.RiskHigh {
		score -= 0.1
	}

	return score
}

func suggestExplain(cap domain.Capability, taskLower string, words []string) string {
	reasons := make([]string, 0, 3)
	nameLower := strings.ToLower(cap.Name)

	if strings.Contains(taskLower, nameLower) {
		reasons = append(reasons, "exact name match")
	}

	matchedWords := make([]string, 0, len(words))
	for _, word := range words {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(nameLower, word) || strings.Contains(strings.ToLower(cap.Description), word) {
			matchedWords = append(matchedWords, word)
		}
	}
	if len(matchedWords) > 0 {
		reasons = append(reasons, fmt.Sprintf("matches: %s", strings.Join(matchedWords, ", ")))
	}

	if cap.RiskLevel == domain.RiskLow {
		reasons = append(reasons, "low risk")
	}

	if len(reasons) == 0 {
		return "general relevance"
	}
	return strings.Join(reasons, "; ")
}
