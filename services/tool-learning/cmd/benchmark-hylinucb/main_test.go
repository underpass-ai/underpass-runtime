package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func TestExtractJSON_ValidObject(t *testing.T) {
	input := `Some text before {"tool": "fs.write_file", "args": {"path": "main.go"}} and after`
	result := extractJSON(input)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["tool"] != "fs.write_file" {
		t.Errorf("tool = %v, want fs.write_file", result["tool"])
	}
	args, ok := result["args"].(map[string]any)
	if !ok {
		t.Fatal("args should be a map")
	}
	if args["path"] != "main.go" {
		t.Errorf("args.path = %v, want main.go", args["path"])
	}
}

func TestExtractJSON_NestedBraces(t *testing.T) {
	input := `{"outer": {"inner": {"deep": true}}}`
	result := extractJSON(input)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["outer"].(map[string]any); !ok {
		t.Error("outer should be a nested object")
	}
}

func TestExtractJSON_NoBraces(t *testing.T) {
	result := extractJSON("no json here")
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestExtractJSON_UnmatchedBraces(t *testing.T) {
	result := extractJSON("{unclosed")
	if result != nil {
		t.Errorf("expected nil for unclosed brace, got %v", result)
	}
}

func TestExtractJSON_InvalidJSON(t *testing.T) {
	result := extractJSON("{not: valid: json}")
	if result != nil {
		t.Errorf("expected nil for invalid json, got %v", result)
	}
}

func TestExtractJSON_DoneAction(t *testing.T) {
	input := `Here is my response: {"done": true, "summary": "completed the task"}`
	result := extractJSON(input)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if done, _ := result["done"].(bool); !done {
		t.Error("done should be true")
	}
}

func TestJsonStr_NestedKeys(t *testing.T) {
	m := map[string]any{
		"session": map[string]any{
			"id": "abc-123",
		},
	}
	got := jsonStr(m, "session", "id")
	if got != "abc-123" {
		t.Errorf("jsonStr = %q, want %q", got, "abc-123")
	}
}

func TestJsonStr_MissingKey(t *testing.T) {
	m := map[string]any{"a": "b"}
	got := jsonStr(m, "nonexistent", "deep")
	if got != "" {
		t.Errorf("jsonStr = %q, want empty string", got)
	}
}

func TestJsonStr_SingleKey(t *testing.T) {
	m := map[string]any{"status": "ok"}
	got := jsonStr(m, "status")
	if got != "ok" {
		t.Errorf("jsonStr = %q, want %q", got, "ok")
	}
}

func TestJsonStr_NonStringValue(t *testing.T) {
	m := map[string]any{"count": 42}
	got := jsonStr(m, "count")
	if got != "" {
		t.Errorf("jsonStr should return empty for non-string, got %q", got)
	}
}

func TestJsonArr(t *testing.T) {
	m := map[string]any{
		"tools": []any{"a", "b", "c"},
	}
	got := jsonArr(m, "tools")
	if len(got) != 3 {
		t.Errorf("jsonArr length = %d, want 3", len(got))
	}
}

func TestJsonArr_Missing(t *testing.T) {
	m := map[string]any{}
	got := jsonArr(m, "tools")
	if got != nil {
		t.Errorf("jsonArr should return nil for missing key, got %v", got)
	}
}

func TestJsonArr_WrongType(t *testing.T) {
	m := map[string]any{"tools": "not-an-array"}
	got := jsonArr(m, "tools")
	if got != nil {
		t.Errorf("jsonArr should return nil for wrong type, got %v", got)
	}
}

func TestEnvOr_Fallback(t *testing.T) {
	got := envOr("BENCHMARK_TEST_UNLIKELY_VAR_XYZ", "default-val")
	if got != "default-val" {
		t.Errorf("envOr = %q, want %q", got, "default-val")
	}
}

func TestEnvOr_EnvSet(t *testing.T) {
	t.Setenv("BENCHMARK_TEST_VAR", "from-env")
	got := envOr("BENCHMARK_TEST_VAR", "fallback")
	if got != "from-env" {
		t.Errorf("envOr = %q, want %q", got, "from-env")
	}
}

func TestApiCall_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	result, err := apiCall(srv.Client(), "GET", srv.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v, want ok", result["status"])
	}
}

func TestApiCall_POST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session": map[string]any{"id": "sess-001"},
			"echo":    req["name"],
		})
	}))
	defer srv.Close()

	result, err := apiCall(srv.Client(), "POST", srv.URL+"/sessions", map[string]any{"name": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if jsonStr(result, "session", "id") != "sess-001" {
		t.Errorf("session.id = %v, want sess-001", jsonStr(result, "session", "id"))
	}
}

func TestApiCall_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "fail"}`))
	}))
	defer srv.Close()

	result, err := apiCall(srv.Client(), "GET", srv.URL+"/fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["error"] != "fail" {
		t.Errorf("error = %v, want fail", result["error"])
	}
}

func TestApiCall_ConnectionRefused(t *testing.T) {
	_, err := apiCall(&http.Client{}, "GET", "http://127.0.0.1:1", nil)
	if err == nil {
		t.Error("expected error for refused connection")
	}
}

func TestVllmChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["model"] == nil {
			t.Error("expected model in request")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": `{"tool": "fs.write_file", "args": {"path": "test.go"}}`,
					},
				},
			},
		})
	}))
	defer srv.Close()

	// Override vllmURL and vllmModel for test.
	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	messages := []map[string]any{
		{"role": "user", "content": "test"},
	}

	resp, err := vllmChat(srv.Client(), messages)
	if err != nil {
		t.Fatal(err)
	}
	result := extractJSON(resp)
	if result == nil {
		t.Fatal("expected JSON in response")
	}
	if result["tool"] != "fs.write_file" {
		t.Errorf("tool = %v, want fs.write_file", result["tool"])
	}
}

func TestVllmChat_ThinkingTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": `<think>reasoning here</think>{"done": true, "summary": "ok"}`,
					},
				},
			},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	resp, err := vllmChat(srv.Client(), []map[string]any{{"role": "user", "content": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	result := extractJSON(resp)
	if result == nil {
		t.Fatal("expected JSON after stripping thinking tags")
	}
	if done, _ := result["done"].(bool); !done {
		t.Error("done should be true")
	}
}

func TestVllmChat_MarkdownFences(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "```json\n{\"tool\": \"git.commit\"}\n```",
					},
				},
			},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	resp, err := vllmChat(srv.Client(), []map[string]any{{"role": "user", "content": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	result := extractJSON(resp)
	if result == nil {
		t.Fatal("expected JSON after stripping markdown fences")
	}
	if result["tool"] != "git.commit" {
		t.Errorf("tool = %v, want git.commit", result["tool"])
	}
}

func TestVllmChat_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	_, err := vllmChat(srv.Client(), []map[string]any{{"role": "user", "content": "test"}})
	if err == nil {
		t.Error("expected error for no choices")
	}
}

func TestVllmChat_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	_, err := vllmChat(srv.Client(), []map[string]any{{"role": "user", "content": "test"}})
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestVllmAgentLoop_ToolInvocation(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/chat/completions" {
			callCount++
			var content string
			if callCount == 1 {
				content = `{"tool": "fs.write_file", "args": {"path": "main.go", "content": "package main"}, "approved": true}`
			} else {
				content = `{"done": true, "summary": "created file"}`
			}
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"content": content}},
				},
			})
			return
		}

		// Tool invocation endpoint.
		json.NewEncoder(w).Encode(map[string]any{
			"invocation": map[string]any{"status": "succeeded"},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	origRuntime := *runtimeURL
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	*runtimeURL = srv.URL
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
		*runtimeURL = origRuntime
	}()

	tools := []any{map[string]any{"name": "fs.write_file"}}
	invocations := vllmAgentLoop(srv.Client(), "sess-1", "create a file", tools)

	if len(invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(invocations))
	}
	if invocations[0].tool != "fs.write_file" {
		t.Errorf("tool = %s, want fs.write_file", invocations[0].tool)
	}
	if invocations[0].status != "succeeded" {
		t.Errorf("status = %s, want succeeded", invocations[0].status)
	}
}

func TestVllmAgentLoop_NoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{"content": "I don't understand"}},
			},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	invocations := vllmAgentLoop(srv.Client(), "sess-1", "test", nil)
	if len(invocations) != 0 {
		t.Errorf("expected 0 invocations, got %d", len(invocations))
	}
}

func TestVllmChat_ReasoningFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content":   "",
						"reasoning": `{"done": true, "summary": "used reasoning"}`,
					},
				},
			},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	resp, err := vllmChat(srv.Client(), []map[string]any{{"role": "user", "content": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	result := extractJSON(resp)
	if result == nil {
		t.Fatal("expected JSON from reasoning fallback")
	}
	if done, _ := result["done"].(bool); !done {
		t.Error("done should be true")
	}
}

func TestVllmAgentLoop_LLMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Return invalid response to trigger LLM error.
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	invocations := vllmAgentLoop(srv.Client(), "sess-1", "test", nil)
	if len(invocations) != 0 {
		t.Errorf("expected 0 invocations on LLM error, got %d", len(invocations))
	}
}

func TestVllmAgentLoop_EmptyTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			// Return action with empty tool name.
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"content": `{"tool": "", "args": {}}`}},
				},
			})
			return
		}
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
	}()

	invocations := vllmAgentLoop(srv.Client(), "sess-1", "test", nil)
	if len(invocations) != 0 {
		t.Errorf("expected 0 invocations for empty tool, got %d", len(invocations))
	}
}

func TestVllmAgentLoop_InvokeError(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{
						"content": `{"tool": "fs.write_file", "args": {}, "approved": true}`,
					}},
				},
			})
			return
		}
		// Invoke endpoint — fail on purpose.
		callCount++
		http.Error(w, "connection refused", http.StatusBadGateway)
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	origRuntime := *runtimeURL
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	// Use an unreachable address so invoke fails with connection error.
	*runtimeURL = "http://127.0.0.1:1"
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
		*runtimeURL = origRuntime
	}()

	invocations := vllmAgentLoop(srv.Client(), "sess-1", "test", nil)
	if len(invocations) != 1 {
		t.Fatalf("expected 1 invocation (error), got %d", len(invocations))
	}
	if invocations[0].status != "error" {
		t.Errorf("status = %s, want error", invocations[0].status)
	}
}

func TestVllmAgentLoop_MultipleIterations(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			callCount++
			var content string
			switch callCount {
			case 1:
				content = `{"tool": "fs.write_file", "args": {"path": "a.go"}, "approved": true}`
			case 2:
				content = `{"tool": "fs.read_file", "args": {"path": "a.go"}, "approved": true}`
			default:
				content = `{"done": true, "summary": "all done"}`
			}
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"content": content}},
				},
			})
			return
		}
		// Tool invocation — succeed.
		json.NewEncoder(w).Encode(map[string]any{
			"invocation": map[string]any{"status": "succeeded"},
		})
	}))
	defer srv.Close()

	origURL := *vllmURL
	origModel := *vllmModel
	origRuntime := *runtimeURL
	*vllmURL = srv.URL
	*vllmModel = "test-model"
	*runtimeURL = srv.URL
	defer func() {
		*vllmURL = origURL
		*vllmModel = origModel
		*runtimeURL = origRuntime
	}()

	invocations := vllmAgentLoop(srv.Client(), "sess-1", "test task", []any{
		map[string]any{"name": "fs.write_file"},
		map[string]any{"name": "fs.read_file"},
	})

	if len(invocations) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(invocations))
	}
	if invocations[0].tool != "fs.write_file" || invocations[1].tool != "fs.read_file" {
		t.Errorf("tools = [%s, %s], want [fs.write_file, fs.read_file]",
			invocations[0].tool, invocations[1].tool)
	}
}

func TestVerifyDifferentiation_Different(t *testing.T) {
	rankings := []rankEvidence{
		{Context: "go", TopK: []domain.ToolScore{{ToolID: "fs.write_file", Score: 0.9}}},
		{Context: "python", TopK: []domain.ToolScore{{ToolID: "git.commit", Score: 0.8}}},
	}
	if !verifyDifferentiation(rankings) {
		t.Error("expected differentiation=true for different tools")
	}
}

func TestVerifyDifferentiation_Same(t *testing.T) {
	rankings := []rankEvidence{
		{Context: "go", TopK: []domain.ToolScore{{ToolID: "fs.write_file", Score: 0.9}}},
		{Context: "python", TopK: []domain.ToolScore{{ToolID: "fs.write_file", Score: 0.8}}},
	}
	if verifyDifferentiation(rankings) {
		t.Error("expected differentiation=false for same tool")
	}
}

func TestVerifyDifferentiation_TooFewRankings(t *testing.T) {
	if verifyDifferentiation(nil) {
		t.Error("expected false for nil rankings")
	}
	if verifyDifferentiation([]rankEvidence{{Context: "go"}}) {
		t.Error("expected false for single ranking")
	}
}

func TestVerifyDifferentiation_EmptyTopK(t *testing.T) {
	rankings := []rankEvidence{
		{Context: "go", TopK: nil},
		{Context: "python", TopK: []domain.ToolScore{{ToolID: "x", Score: 1}}},
	}
	if verifyDifferentiation(rankings) {
		t.Error("expected false for empty top-k")
	}
}

func TestWriteEvidence_ToFile(t *testing.T) {
	ev := evidence{
		TestID: "test-1",
		Status: "passed",
	}
	path := t.TempDir() + "/evidence.json"
	err := writeEvidence(ev, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded evidence
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.TestID != "test-1" || loaded.Status != "passed" {
		t.Errorf("loaded = %+v", loaded)
	}
}

func TestWriteEvidence_ToStdout(t *testing.T) {
	ev := evidence{TestID: "test-2", Status: "passed"}
	// Empty path writes to stdout — just check no error.
	err := writeEvidence(ev, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteEvidence_InvalidPath(t *testing.T) {
	ev := evidence{TestID: "test-3"}
	err := writeEvidence(ev, "/nonexistent/dir/file.json")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestSection(t *testing.T) {
	// Just verify it doesn't panic.
	section("Test Section")
}
