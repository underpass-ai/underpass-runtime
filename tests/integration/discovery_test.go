//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
)

func TestTools_ListTools(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		decodeJSON(t, resp, &body)

		if len(body.Tools) == 0 {
			t.Fatal("expected at least one tool")
		}

		toolNames := make(map[string]bool)
		for _, tool := range body.Tools {
			toolNames[tool.Name] = true
		}
		for _, expected := range []string{"fs.list", "fs.read_file", "git.status", "repo.build"} {
			if !toolNames[expected] {
				t.Errorf("expected tool %s in list", expected)
			}
		}
	})
}

func TestTools_ListToolsInvalidSession(t *testing.T) {
	resp := doGet(t, "/v1/sessions/does-not-exist/tools")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestDiscovery_CompactDefault(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Risk        string   `json:"risk"`
				SideEffects string   `json:"side_effects"`
				Approval    bool     `json:"approval"`
				Tags        []string `json:"tags"`
				Cost        string   `json:"cost"`
			} `json:"tools"`
			Total    int `json:"total"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Total == 0 {
			t.Fatal("expected total > 0")
		}
		if body.Filtered == 0 {
			t.Fatal("expected filtered > 0")
		}
		if body.Filtered > body.Total {
			t.Fatalf("filtered (%d) should be <= total (%d)", body.Filtered, body.Total)
		}

		for _, tool := range body.Tools {
			if tool.Name == "" {
				t.Error("compact tool missing name")
			}
			if tool.Description == "" {
				t.Errorf("compact tool %s missing description", tool.Name)
			}
		}
	})
}

func TestDiscovery_FullDetail(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?detail=full", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema any    `json:"input_schema"`
				Scope       string `json:"scope"`
			} `json:"tools"`
			Total    int `json:"total"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Total == 0 {
			t.Fatal("expected total > 0")
		}

		found := false
		for _, tool := range body.Tools {
			if tool.Name == "fs.list" {
				found = true
				if tool.InputSchema == nil {
					t.Error("full detail should include input_schema")
				}
				if tool.Scope == "" {
					t.Error("full detail should include scope")
				}
				break
			}
		}
		if !found {
			t.Error("expected fs.list in full discovery")
		}
	})
}

func TestDiscovery_FilterByRisk(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?risk=low", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name string `json:"name"`
				Risk string `json:"risk"`
			} `json:"tools"`
			Total    int `json:"total"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Filtered == 0 {
			t.Fatal("expected some low-risk tools")
		}
		if body.Filtered > body.Total {
			t.Fatalf("filtered (%d) should be <= total (%d)", body.Filtered, body.Total)
		}
		for _, tool := range body.Tools {
			if tool.Risk != "low" {
				t.Errorf("tool %s has risk %s, expected low", tool.Name, tool.Risk)
			}
		}
	})
}

func TestDiscovery_FilterByCost(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?cost=low", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name string `json:"name"`
				Cost string `json:"cost"`
			} `json:"tools"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Filtered == 0 {
			t.Fatal("expected some low-cost tools")
		}
		for _, tool := range body.Tools {
			if tool.Cost != "low" {
				t.Errorf("tool %s has cost %s, expected low", tool.Name, tool.Cost)
			}
		}
	})
}

func TestDiscovery_FilterBySideEffects(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?side_effects=none", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name        string `json:"name"`
				SideEffects string `json:"side_effects"`
			} `json:"tools"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Filtered == 0 {
			t.Fatal("expected some tools with no side effects")
		}
		for _, tool := range body.Tools {
			if tool.SideEffects != "none" {
				t.Errorf("tool %s has side_effects %s, expected none", tool.Name, tool.SideEffects)
			}
		}
	})
}

func TestDiscovery_FilterByScope(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?scope=repo", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools    []struct{ Name string `json:"name"` } `json:"tools"`
			Filtered int                                    `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		if body.Filtered == 0 {
			t.Fatal("expected some repo-scoped tools")
		}
	})
}

func TestDiscovery_CombinedFilters(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/discovery?risk=low&side_effects=none", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Tools []struct {
				Name        string `json:"name"`
				Risk        string `json:"risk"`
				SideEffects string `json:"side_effects"`
			} `json:"tools"`
			Filtered int `json:"filtered"`
		}
		decodeJSON(t, resp, &body)

		for _, tool := range body.Tools {
			if tool.Risk != "low" {
				t.Errorf("tool %s: risk=%s, expected low", tool.Name, tool.Risk)
			}
			if tool.SideEffects != "none" {
				t.Errorf("tool %s: side_effects=%s, expected none", tool.Name, tool.SideEffects)
			}
		}
	})
}

func TestDiscovery_InvalidSession(t *testing.T) {
	resp := doGet(t, "/v1/sessions/does-not-exist/tools/discovery")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestRecommendations_WithTaskHint(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/recommendations?task_hint=run+tests&top_k=5", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Recommendations []struct {
				Name  string  `json:"name"`
				Score float64 `json:"score"`
				Why   string  `json:"why"`
			} `json:"recommendations"`
			TaskHint string `json:"task_hint"`
			TopK     int    `json:"top_k"`
		}
		decodeJSON(t, resp, &body)

		if body.TaskHint != "run tests" {
			t.Fatalf("expected task_hint='run tests', got '%s'", body.TaskHint)
		}
		if len(body.Recommendations) == 0 {
			t.Fatal("expected at least one recommendation")
		}
		if len(body.Recommendations) > 5 {
			t.Fatalf("expected at most 5 recommendations, got %d", len(body.Recommendations))
		}
		// Verify sorted by score descending
		for i := 1; i < len(body.Recommendations); i++ {
			if body.Recommendations[i].Score > body.Recommendations[i-1].Score {
				t.Errorf("recommendations not sorted: [%d].score=%f > [%d].score=%f",
					i, body.Recommendations[i].Score, i-1, body.Recommendations[i-1].Score)
			}
		}
	})
}

func TestRecommendations_DefaultTopK(t *testing.T) {
	withSession(t, func(sid string) {
		resp := doGet(t, fmt.Sprintf("/v1/sessions/%s/tools/recommendations?task_hint=build", sid))
		expectStatus(t, resp, http.StatusOK)

		var body struct {
			Recommendations []struct{ Name string `json:"name"` } `json:"recommendations"`
		}
		decodeJSON(t, resp, &body)

		if len(body.Recommendations) == 0 {
			t.Fatal("expected recommendations for build task")
		}
	})
}

func TestRecommendations_InvalidSession(t *testing.T) {
	resp := doGet(t, "/v1/sessions/does-not-exist/tools/recommendations?task_hint=build")
	expectStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
