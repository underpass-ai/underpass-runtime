package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testModeArrivalRate         = "arrival_rate"
	testConstraintsViolationMsg = "constraints violation"
)

type fakeBenchmarkRunner struct {
	calls []app.CommandSpec
	run   func(callIndex int, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeBenchmarkRunner) Run(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(len(f.calls)-1, spec)
	}
	return app.CommandResult{ExitCode: 0}, nil
}

func TestAPIBenchmarkHandler_Success(t *testing.T) {
	configureBenchmarkEndpointEnv(t)

	workspace := t.TempDir()
	runner := &fakeBenchmarkRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "k6" {
				t.Fatalf("unexpected command: %s", spec.Command)
			}
			args := strings.Join(spec.Args, " ")
			if !strings.Contains(args, "--summary-export .bench/summary.json") {
				t.Fatalf("unexpected k6 args: %v", spec.Args)
			}
			if len(spec.Stdin) == 0 || !strings.Contains(string(spec.Stdin), "constant-vus") {
				t.Fatalf("expected constant-vus script, got: %q", string(spec.Stdin))
			}

			writeBenchmarkSummary(t, workspace)
			return app.CommandResult{ExitCode: 0, Output: "k6 completed"}, nil
		},
	}

	handler := NewAPIBenchmarkHandler(runner)
	session := writableBenchmarkSession(workspace)
	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{
			"profile_id":"bench.workspace",
			"request":{"method":"GET","path":"/healthz","headers":{"accept":"application/json"}},
			"load":{"mode":"constant_vus","duration_ms":10000,"vus":5},
			"thresholds":{"p95_ms":300,"error_rate":0.05,"checks":0.95}
		}`),
	)
	if err != nil {
		t.Fatalf("unexpected api.benchmark error: %#v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf(testExpectedMapOutputFmt, result.Output)
	}
	assertBenchmarkOutputFields(t, output)
	assertBenchmarkArtifacts(t, result)
}

func writeBenchmarkSummary(t *testing.T, workspace string) {
	t.Helper()
	summary := `{
  "metrics": {
    "http_req_duration": {
      "min": 2.1, "avg": 10.4, "med": 9.0, "p(95)": 20.7, "p(99)": 31.2, "max": 40.1,
      "thresholds": {"p(95)<300": true}
    },
    "http_reqs": {"count": 80, "rate": 8.0},
    "http_req_failed": {"rate": 0.0125, "fails": 1, "passes": 79, "thresholds": {"rate<0.05": true}},
    "checks": {"value": 0.99, "passes": 79, "fails": 1, "thresholds": {"rate>0.95": true}},
    "bench_http_code_200": {"count": 79, "rate": 7.9},
    "bench_http_code_500": {"count": 1, "rate": 0.1}
  }
}`
	if err := os.MkdirAll(filepath.Join(workspace, ".bench"), 0o755); err != nil {
		t.Fatalf("mkdir .bench: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".bench", "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
}

func assertBenchmarkOutputFields(t *testing.T, output map[string]any) {
	t.Helper()
	if output["requests"] != 80 {
		t.Fatalf("unexpected requests: %#v", output["requests"])
	}
	if output["failed_requests"] != 1 {
		t.Fatalf("unexpected failed_requests: %#v", output["failed_requests"])
	}
	thresholds, ok := output["thresholds"].(map[string]any)
	if !ok {
		t.Fatalf("expected thresholds map, got %#v", output["thresholds"])
	}
	if thresholds["passed"] != true {
		t.Fatalf("expected passed=true, got %#v", thresholds["passed"])
	}
	codes := mapStringInt(output["http_codes"])
	if codes["200"] != 79 || codes["500"] != 1 {
		t.Fatalf("unexpected http codes: %#v", codes)
	}
}

func assertBenchmarkArtifacts(t *testing.T, result app.ToolRunResult) {
	t.Helper()
	artifactNames := make([]string, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		artifactNames = append(artifactNames, artifact.Name)
	}
	if !containsString(artifactNames, "benchmark-summary.json") ||
		!containsString(artifactNames, "benchmark-k6.js") ||
		!containsString(artifactNames, "benchmark-k6.log") {
		t.Fatalf("missing benchmark artifacts: %#v", artifactNames)
	}
}

func TestAPIBenchmarkHandler_DeniesRouteOutsideProfileScopes(t *testing.T) {
	configureBenchmarkEndpointEnv(t)

	handler := NewAPIBenchmarkHandler(&fakeBenchmarkRunner{})
	session := writableBenchmarkSession(t.TempDir())

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"bench.workspace","request":{"method":"GET","path":"/private"}}`),
	)
	if err == nil {
		t.Fatal("expected route policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestAPIBenchmarkHandler_DeniesReadOnlyUnsafeMethod(t *testing.T) {
	configureBenchmarkEndpointEnv(t)

	handler := NewAPIBenchmarkHandler(&fakeBenchmarkRunner{})
	session := writableBenchmarkSession(t.TempDir())
	session.Metadata["connection_profiles_json"] = `[{"id":"bench.workspace","kind":"http","read_only":true,"scopes":{"routes":["/healthz"]}}]`

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"bench.workspace","request":{"method":"POST","path":"/healthz","body":"x"}}`),
	)
	if err == nil {
		t.Fatal("expected read_only policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestAPIBenchmarkHandler_RejectsConstraintsViolation(t *testing.T) {
	configureBenchmarkEndpointEnv(t)

	handler := NewAPIBenchmarkHandler(&fakeBenchmarkRunner{})
	session := writableBenchmarkSession(t.TempDir())

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"bench.workspace","request":{"method":"GET","path":"/healthz"},"load":{"mode":"constant_vus","duration_ms":10000,"vus":500}}`),
	)
	if err == nil {
		t.Fatal("expected constraints violation")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if !strings.Contains(err.Message, testConstraintsViolationMsg) {
		t.Fatalf("unexpected constraints message: %q", err.Message)
	}
}

func TestAPIBenchmarkHandler_ExecutionError(t *testing.T) {
	configureBenchmarkEndpointEnv(t)

	runner := &fakeBenchmarkRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 127, Output: "k6: not found"}, errors.New("exit status 127")
		},
	}
	handler := NewAPIBenchmarkHandler(runner)
	session := writableBenchmarkSession(t.TempDir())

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"profile_id":"bench.workspace","request":{"method":"GET","path":"/healthz"}}`),
	)
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestAPIBenchmarkHandler_Name(t *testing.T) {
	if NewAPIBenchmarkHandler(nil).Name() != "api.benchmark" {
		t.Fatal("unexpected api.benchmark name")
	}
}

func writableBenchmarkSession(workspace string) domain.Session {
	return domain.Session{
		WorkspacePath: workspace,
		AllowedPaths:  []string{"."},
		Metadata: map[string]string{
			"allowed_profiles":         "bench.workspace",
			"connection_profiles_json": `[{"id":"bench.workspace","kind":"http","read_only":false,"scopes":{"routes":["/healthz","/bench/","regex:^/delay/[0-9]+$"]}}]`,
		},
	}
}

func configureBenchmarkEndpointEnv(t *testing.T) {
	t.Helper()
	t.Setenv(
		"WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON",
		`{"bench.workspace":"http://workspace.underpass-runtime.svc.cluster.local:50053"}`,
	)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func mapStringInt(raw any) map[string]int {
	out := map[string]int{}
	switch typed := raw.(type) {
	case map[string]int:
		for key, value := range typed {
			out[key] = value
		}
	case map[string]any:
		for key, value := range typed {
			switch num := value.(type) {
			case int:
				out[key] = num
			case float64:
				out[key] = int(num)
			}
		}
	}
	return out
}

func TestNormalizeArrivalRateLoad_ValidDefaults(t *testing.T) {
	// valid: mode="arrival_rate", durationMS=5000, rps=10, vus=0 -> vus defaults to rps (10), no error
	spec, err := normalizeArrivalRateLoad(testModeArrivalRate, 5000, 10, 0)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if spec.Mode != testModeArrivalRate {
		t.Fatalf("expected mode 'arrival_rate', got %q", spec.Mode)
	}
	if spec.DurationMS != 5000 {
		t.Fatalf("expected durationMS=5000, got %d", spec.DurationMS)
	}
	if spec.RPS != 10 {
		t.Fatalf("expected RPS=10, got %d", spec.RPS)
	}
	// vus defaults to rps when 0
	if spec.VUs != 10 {
		t.Fatalf("expected VUs=10 (defaulted from rps), got %d", spec.VUs)
	}
}

func TestNormalizeArrivalRateLoad_RPSTooLow(t *testing.T) {
	_, err := normalizeArrivalRateLoad(testModeArrivalRate, 5000, 0, 0)
	if err == nil {
		t.Fatal("expected error for rps < 1")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf(testExpectedInvalidArgumentFmt, err.Code)
	}
}

func TestNormalizeArrivalRateLoad_RPSTooHigh(t *testing.T) {
	_, err := normalizeArrivalRateLoad(testModeArrivalRate, 5000, benchmarkMaxRPS+1, 0)
	if err == nil {
		t.Fatal("expected error for rps > max")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf(testExpectedInvalidArgumentFmt, err.Code)
	}
	if !strings.Contains(err.Message, testConstraintsViolationMsg) {
		t.Fatalf("expected constraints violation message, got %q", err.Message)
	}
}

func TestNormalizeArrivalRateLoad_VUSTooHigh(t *testing.T) {
	_, err := normalizeArrivalRateLoad(testModeArrivalRate, 5000, 10, benchmarkMaxVUs+1)
	if err == nil {
		t.Fatal("expected error for vus > max")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf(testExpectedInvalidArgumentFmt, err.Code)
	}
	if !strings.Contains(err.Message, testConstraintsViolationMsg) {
		t.Fatalf("expected constraints violation message, got %q", err.Message)
	}
}

func TestNormalizeArrivalRateLoad_ExplicitVUs(t *testing.T) {
	// valid with explicit vus=20 -> vus=20
	spec, err := normalizeArrivalRateLoad(testModeArrivalRate, 5000, 10, 20)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if spec.VUs != 20 {
		t.Fatalf("expected VUs=20, got %d", spec.VUs)
	}
	if spec.RPS != 10 {
		t.Fatalf("expected RPS=10, got %d", spec.RPS)
	}
}

// ---------------------------------------------------------------------------
// asFloat — covers all type-switch branches
// ---------------------------------------------------------------------------

func TestAsFloat_AllBranches(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		want    float64
		wantOK  bool
	}{
		{name: "float64", input: float64(3.14), want: 3.14, wantOK: true},
		{name: "float32", input: float32(2.5), want: float64(float32(2.5)), wantOK: true},
		{name: "int", input: int(42), want: 42, wantOK: true},
		{name: "int64", input: int64(100), want: 100, wantOK: true},
		{name: "int32", input: int32(200), want: 200, wantOK: true},
		{name: "uint", input: uint(10), want: 10, wantOK: true},
		{name: "uint64", input: uint64(999), want: 999, wantOK: true},
		{name: "json.Number_valid", input: json.Number("1.23"), want: 1.23, wantOK: true},
		{name: "json.Number_invalid", input: json.Number("not_a_number"), want: 0, wantOK: false},
		{name: "nil", input: nil, want: 0, wantOK: false},
		{name: "string", input: "hello", want: 0, wantOK: false},
		{name: "bool", input: true, want: 0, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := asFloat(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("asFloat(%v) ok=%v, want %v", tc.input, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("asFloat(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// metricFloat / metricInt — nested "values" key and direct key
// ---------------------------------------------------------------------------

func TestMetricFloat_NestedValuesKey(t *testing.T) {
	metric := map[string]any{
		"values": map[string]any{
			"avg": float64(10.5),
		},
	}
	if got := metricFloat(metric, "avg"); got != 10.5 {
		t.Fatalf("expected 10.5, got %v", got)
	}
}

func TestMetricFloat_DirectKey(t *testing.T) {
	metric := map[string]any{
		"count": float64(42),
	}
	if got := metricFloat(metric, "count"); got != 42 {
		t.Fatalf("expected 42, got %v", got)
	}
}

func TestMetricFloat_NilMetric(t *testing.T) {
	if got := metricFloat(nil, "anything"); got != 0 {
		t.Fatalf("expected 0 for nil metric, got %v", got)
	}
}

func TestMetricFloat_EmptyMetric(t *testing.T) {
	if got := metricFloat(map[string]any{}, "key"); got != 0 {
		t.Fatalf("expected 0 for empty metric, got %v", got)
	}
}

func TestMetricFloat_ValuesNotMap(t *testing.T) {
	// "values" exists but is not map[string]any → falls through to direct key lookup
	metric := map[string]any{
		"values": "not a map",
		"rate":   float64(7.7),
	}
	if got := metricFloat(metric, "rate"); got != 7.7 {
		t.Fatalf("expected 7.7, got %v", got)
	}
}

func TestMetricInt_RoundsCorrectly(t *testing.T) {
	metric := map[string]any{"count": float64(3.7)}
	if got := metricInt(metric, "count"); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
	metric2 := map[string]any{"count": float64(3.2)}
	if got := metricInt(metric2, "count"); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// extractBenchmarkProfileStringList — all branches
// ---------------------------------------------------------------------------

func TestExtractBenchmarkProfileStringList(t *testing.T) {
	t.Run("nil_scopes", func(t *testing.T) {
		result := extractBenchmarkProfileStringList(nil, "routes")
		if result != nil {
			t.Fatalf("expected nil, got %v", result)
		}
	})
	t.Run("key_not_found", func(t *testing.T) {
		result := extractBenchmarkProfileStringList(map[string]any{"other": "val"}, "routes")
		if result != nil {
			t.Fatalf("expected nil, got %v", result)
		}
	})
	t.Run("string_slice", func(t *testing.T) {
		scopes := map[string]any{
			"routes": []string{"/healthz", "  ", "/api/"},
		}
		result := extractBenchmarkProfileStringList(scopes, "routes")
		// empty strings should be filtered out
		if len(result) != 2 || result[0] != "/healthz" || result[1] != "/api/" {
			t.Fatalf("unexpected result: %v", result)
		}
	})
	t.Run("any_slice", func(t *testing.T) {
		scopes := map[string]any{
			"routes": []any{"/healthz", 42, " ", "/bench/"},
		}
		result := extractBenchmarkProfileStringList(scopes, "routes")
		// non-strings and blank strings filtered
		if len(result) != 2 || result[0] != "/healthz" || result[1] != "/bench/" {
			t.Fatalf("unexpected result: %v", result)
		}
	})
	t.Run("unsupported_type", func(t *testing.T) {
		scopes := map[string]any{
			"routes": 12345,
		}
		result := extractBenchmarkProfileStringList(scopes, "routes")
		if result != nil {
			t.Fatalf("expected nil for unsupported type, got %v", result)
		}
	})
}

// ---------------------------------------------------------------------------
// routeAllowedByProfile — full coverage
// ---------------------------------------------------------------------------

func TestRouteAllowedByProfile(t *testing.T) {
	t.Run("no_routes_or_paths", func(t *testing.T) {
		profile := connectionProfile{Scopes: map[string]any{}}
		if routeAllowedByProfile("/foo", profile) {
			t.Fatal("expected deny with no routes/paths")
		}
	})
	t.Run("uses_paths_fallback", func(t *testing.T) {
		profile := connectionProfile{Scopes: map[string]any{
			"paths": []any{"/foo"},
		}}
		if !routeAllowedByProfile("/foo", profile) {
			t.Fatal("expected allow via paths fallback")
		}
	})
	t.Run("no_match", func(t *testing.T) {
		profile := connectionProfile{Scopes: map[string]any{
			"routes": []any{"/healthz"},
		}}
		if routeAllowedByProfile("/admin", profile) {
			t.Fatal("expected deny for non-matching route")
		}
	})
}

// ---------------------------------------------------------------------------
// routePatternMatch — all branches
// ---------------------------------------------------------------------------

func TestRoutePatternMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{name: "empty_pattern", pattern: "", path: "/foo", want: false},
		{name: "empty_path", pattern: "/foo", path: "", want: false},
		{name: "wildcard_star", pattern: "*", path: "/anything", want: true},
		{name: "exact_match", pattern: "/healthz", path: "/healthz", want: true},
		{name: "regex_match", pattern: "regex:^/delay/[0-9]+$", path: "/delay/123", want: true},
		{name: "regex_no_match", pattern: "regex:^/delay/[0-9]+$", path: "/delay/abc", want: false},
		{name: "regex_empty", pattern: "regex:", path: "/foo", want: false},
		{name: "regex_invalid", pattern: "regex:[invalid", path: "/foo", want: false},
		{name: "glob_match", pattern: "/api/*/items", path: "/api/v1/items", want: true},
		{name: "glob_no_match", pattern: "/api/*/items", path: "/api/v1/other", want: false},
		{name: "prefix_slash", pattern: "/bench/", path: "/bench/something", want: true},
		{name: "prefix_slash_no_match", pattern: "/bench/", path: "/other/something", want: false},
		{name: "no_match_no_glob_no_prefix", pattern: "/exact", path: "/other", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := routePatternMatch(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("routePatternMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// k6ScenarioJSON — both modes
// ---------------------------------------------------------------------------

func TestK6ScenarioJSON_ConstantVUs(t *testing.T) {
	result, err := k6ScenarioJSON(benchmarkLoadSpec{Mode: "constant_vus", VUs: 5, DurationMS: 10000})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !strings.Contains(result, "constant-vus") {
		t.Fatalf("expected constant-vus in scenario JSON, got %s", result)
	}
	if !strings.Contains(result, `"vus":5`) {
		t.Fatalf("expected vus:5 in scenario JSON, got %s", result)
	}
}

func TestK6ScenarioJSON_ArrivalRate(t *testing.T) {
	result, err := k6ScenarioJSON(benchmarkLoadSpec{Mode: "arrival_rate", RPS: 10, DurationMS: 5000, VUs: 0})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !strings.Contains(result, "constant-arrival-rate") {
		t.Fatalf("expected constant-arrival-rate in scenario JSON, got %s", result)
	}
	if !strings.Contains(result, `"rate":10`) {
		t.Fatalf("expected rate:10 in scenario JSON, got %s", result)
	}
	// preAllocated should default to 1 when VUs=0
	if !strings.Contains(result, `"preAllocatedVUs":1`) {
		t.Fatalf("expected preAllocatedVUs:1 when VUs=0, got %s", result)
	}
}

func TestK6ScenarioJSON_ArrivalRateClampsVUs(t *testing.T) {
	// VUs > max should be clamped
	result, err := k6ScenarioJSON(benchmarkLoadSpec{Mode: "arrival_rate", RPS: 10, DurationMS: 5000, VUs: benchmarkMaxVUs + 100})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	expected := fmt.Sprintf(`"preAllocatedVUs":%d`, benchmarkMaxVUs)
	if !strings.Contains(result, expected) {
		t.Fatalf("expected preAllocatedVUs clamped to %d, got %s", benchmarkMaxVUs, result)
	}
}

// ---------------------------------------------------------------------------
// normalizeBenchmarkPath — all validation branches
// ---------------------------------------------------------------------------

func TestNormalizeBenchmarkPath(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantPath  string
		wantPure  string
		wantError string
	}{
		{name: "valid_path", input: "/healthz", wantPath: "/healthz", wantPure: "/healthz"},
		{name: "with_query", input: "/api?key=val", wantPath: "/api?key=val", wantPure: "/api"},
		{name: "empty", input: "", wantError: "request.path is required"},
		{name: "spaces_only", input: "   ", wantError: "request.path is required"},
		{name: "newline", input: "/foo\nbar", wantError: "request.path contains invalid characters"},
		{name: "carriage_return", input: "/foo\rbar", wantError: "request.path contains invalid characters"},
		{name: "absolute_url", input: "http://evil.com/foo", wantError: "request.path must be relative"},
		{name: "no_leading_slash", input: "api/endpoint", wantError: "request.path must start with '/'"},
		{name: "dot_dot", input: "/../../etc/passwd", wantError: "request.path must not contain '..'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relPath, purePath, err := normalizeBenchmarkPath(tc.input)
			if tc.wantError != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantError)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got %q", tc.wantError, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if relPath != tc.wantPath {
				t.Fatalf("expected path %q, got %q", tc.wantPath, relPath)
			}
			if purePath != tc.wantPure {
				t.Fatalf("expected pure path %q, got %q", tc.wantPure, purePath)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validateSingleBenchmarkHeader — all branches
// ---------------------------------------------------------------------------

func TestValidateSingleBenchmarkHeader(t *testing.T) {
	cases := []struct {
		name      string
		hdrName   string
		hdrValue  string
		wantError bool
	}{
		{name: "allowed_accept", hdrName: "accept", hdrValue: "application/json", wantError: false},
		{name: "allowed_x_prefix", hdrName: "x-custom", hdrValue: "value", wantError: false},
		{name: "denied_authorization", hdrName: "Authorization", hdrValue: "Bearer token", wantError: true},
		{name: "denied_cookie", hdrName: "Cookie", hdrValue: "session=abc", wantError: true},
		{name: "not_allowed_unknown", hdrName: "unknown-header", hdrValue: "val", wantError: true},
		{name: "name_with_newline", hdrName: "good\nheader", hdrValue: "val", wantError: true},
		{name: "value_with_newline", hdrName: "accept", hdrValue: "val\ninjected", wantError: true},
		{name: "name_with_cr", hdrName: "good\rheader", hdrValue: "val", wantError: true},
		{name: "value_with_cr", hdrName: "accept", hdrValue: "val\rinjected", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSingleBenchmarkHeader(tc.hdrName, tc.hdrValue)
			if tc.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sanitizeBenchmarkHeaders — higher-level validation
// ---------------------------------------------------------------------------

func TestSanitizeBenchmarkHeaders(t *testing.T) {
	t.Run("empty_map", func(t *testing.T) {
		headers, bytes, err := sanitizeBenchmarkHeaders(nil)
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if len(headers) != 0 || bytes != 0 {
			t.Fatalf("expected empty result, got headers=%v bytes=%d", headers, bytes)
		}
	})
	t.Run("valid_headers", func(t *testing.T) {
		headers, bytes, err := sanitizeBenchmarkHeaders(map[string]string{
			"accept":     "application/json",
			"x-trace-id": "abc123",
		})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if len(headers) != 2 {
			t.Fatalf("expected 2 headers, got %d", len(headers))
		}
		if bytes == 0 {
			t.Fatal("expected non-zero bytes")
		}
	})
	t.Run("too_many_headers", func(t *testing.T) {
		many := make(map[string]string, benchmarkMaxHeaders+1)
		for i := 0; i <= benchmarkMaxHeaders; i++ {
			many[fmt.Sprintf("x-hdr-%d", i)] = "val"
		}
		_, _, err := sanitizeBenchmarkHeaders(many)
		if err == nil {
			t.Fatal("expected error for too many headers")
		}
	})
	t.Run("empty_key_filtered", func(t *testing.T) {
		headers, _, err := sanitizeBenchmarkHeaders(map[string]string{
			"":       "should be dropped",
			"accept": "ok",
		})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if len(headers) != 1 {
			t.Fatalf("expected 1 header after filtering, got %d", len(headers))
		}
	})
}

// ---------------------------------------------------------------------------
// buildBenchmarkTargetURL — all branches
// ---------------------------------------------------------------------------

func TestBuildBenchmarkTargetURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		relPath  string
		want     string
		wantErr  string
	}{
		{name: "simple", endpoint: "http://localhost:8080", relPath: "/healthz", want: "http://localhost:8080/healthz"},
		{name: "auto_http", endpoint: "api.example.com", relPath: "/status", want: "http://api.example.com/status"},
		{name: "with_base_path", endpoint: "http://host/api/v1/", relPath: "/items?q=1", want: "http://host/api/v1/items?q=1"},
		{name: "https", endpoint: "https://secure.example.com", relPath: "/data", want: "https://secure.example.com/data"},
		{name: "empty_endpoint", endpoint: "", relPath: "/foo", wantErr: "benchmark profile endpoint not configured"},
		{name: "bad_scheme", endpoint: "ftp://host/data", relPath: "/foo", wantErr: "benchmark endpoint must use http or https"},
		{name: "empty_path", endpoint: "http://host", relPath: "", wantErr: "request.path is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildBenchmarkTargetURL(tc.endpoint, tc.relPath)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("buildBenchmarkTargetURL(%q, %q) = %q, want %q", tc.endpoint, tc.relPath, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeBenchmarkLoad — both modes and edge cases
// ---------------------------------------------------------------------------

func TestNormalizeBenchmarkLoad(t *testing.T) {
	type loadInput struct {
		Mode       string `json:"mode"`
		DurationMS int    `json:"duration_ms"`
		VUs        int    `json:"vus"`
		RPS        int    `json:"rps"`
	}
	t.Run("default_mode_and_values", func(t *testing.T) {
		spec, err := normalizeBenchmarkLoad(loadInput{})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if spec.Mode != "constant_vus" {
			t.Fatalf("expected default mode constant_vus, got %q", spec.Mode)
		}
		if spec.DurationMS != benchmarkDefaultDurationMS {
			t.Fatalf("expected default duration %d, got %d", benchmarkDefaultDurationMS, spec.DurationMS)
		}
		if spec.VUs != benchmarkDefaultVUs {
			t.Fatalf("expected default VUs %d, got %d", benchmarkDefaultVUs, spec.VUs)
		}
	})
	t.Run("invalid_mode", func(t *testing.T) {
		_, err := normalizeBenchmarkLoad(loadInput{Mode: "ramping"})
		if err == nil {
			t.Fatal("expected error for invalid mode")
		}
	})
	t.Run("duration_too_low", func(t *testing.T) {
		_, err := normalizeBenchmarkLoad(loadInput{Mode: "constant_vus", DurationMS: 50})
		if err == nil {
			t.Fatal("expected error for duration < 100")
		}
	})
	t.Run("duration_too_high", func(t *testing.T) {
		_, err := normalizeBenchmarkLoad(loadInput{Mode: "constant_vus", DurationMS: benchmarkMaxDurationMS + 1})
		if err == nil {
			t.Fatal("expected error for duration > max")
		}
	})
	t.Run("vus_too_low", func(t *testing.T) {
		_, err := normalizeBenchmarkLoad(loadInput{Mode: "constant_vus", DurationMS: 1000, VUs: -1})
		if err == nil {
			t.Fatal("expected error for vus < 1")
		}
	})
	t.Run("vus_too_high", func(t *testing.T) {
		_, err := normalizeBenchmarkLoad(loadInput{Mode: "constant_vus", DurationMS: 1000, VUs: benchmarkMaxVUs + 1})
		if err == nil {
			t.Fatal("expected error for vus > max")
		}
	})
	t.Run("arrival_rate_valid", func(t *testing.T) {
		spec, err := normalizeBenchmarkLoad(loadInput{Mode: "arrival_rate", DurationMS: 5000, RPS: 50, VUs: 10})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if spec.Mode != "arrival_rate" || spec.RPS != 50 || spec.VUs != 10 {
			t.Fatalf("unexpected arrival_rate spec: %+v", spec)
		}
	})
}

// ---------------------------------------------------------------------------
// normalizeBenchmarkThresholds — all branches
// ---------------------------------------------------------------------------

func TestNormalizeBenchmarkThresholds(t *testing.T) {
	type thresholdInput struct {
		P95MS     *float64 `json:"p95_ms"`
		ErrorRate *float64 `json:"error_rate"`
		Checks    *float64 `json:"checks"`
	}

	t.Run("all_nil", func(t *testing.T) {
		result, err := normalizeBenchmarkThresholds(thresholdInput{})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if len(result) != 0 {
			t.Fatalf("expected empty thresholds, got %v", result)
		}
	})
	t.Run("valid_p95", func(t *testing.T) {
		val := 300.0
		result, err := normalizeBenchmarkThresholds(thresholdInput{P95MS: &val})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if _, ok := result["http_req_duration"]; !ok {
			t.Fatal("expected http_req_duration threshold")
		}
	})
	t.Run("p95_zero_fails", func(t *testing.T) {
		val := 0.0
		_, err := normalizeBenchmarkThresholds(thresholdInput{P95MS: &val})
		if err == nil {
			t.Fatal("expected error for p95 <= 0")
		}
	})
	t.Run("valid_error_rate", func(t *testing.T) {
		val := 0.05
		result, err := normalizeBenchmarkThresholds(thresholdInput{ErrorRate: &val})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if _, ok := result["http_req_failed"]; !ok {
			t.Fatal("expected http_req_failed threshold")
		}
	})
	t.Run("error_rate_negative_fails", func(t *testing.T) {
		val := -0.1
		_, err := normalizeBenchmarkThresholds(thresholdInput{ErrorRate: &val})
		if err == nil {
			t.Fatal("expected error for negative error_rate")
		}
	})
	t.Run("error_rate_above_one_fails", func(t *testing.T) {
		val := 1.5
		_, err := normalizeBenchmarkThresholds(thresholdInput{ErrorRate: &val})
		if err == nil {
			t.Fatal("expected error for error_rate > 1")
		}
	})
	t.Run("valid_checks", func(t *testing.T) {
		val := 0.95
		result, err := normalizeBenchmarkThresholds(thresholdInput{Checks: &val})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if _, ok := result["checks"]; !ok {
			t.Fatal("expected checks threshold")
		}
	})
	t.Run("checks_negative_fails", func(t *testing.T) {
		val := -0.1
		_, err := normalizeBenchmarkThresholds(thresholdInput{Checks: &val})
		if err == nil {
			t.Fatal("expected error for negative checks")
		}
	})
	t.Run("checks_above_one_fails", func(t *testing.T) {
		val := 1.5
		_, err := normalizeBenchmarkThresholds(thresholdInput{Checks: &val})
		if err == nil {
			t.Fatal("expected error for checks > 1")
		}
	})
}

// ---------------------------------------------------------------------------
// evaluateBenchmarkThresholds — all threshold violations
// ---------------------------------------------------------------------------

func TestEvaluateBenchmarkThresholds(t *testing.T) {
	type rawThresholds struct {
		P95MS     *float64 `json:"p95_ms"`
		ErrorRate *float64 `json:"error_rate"`
		Checks    *float64 `json:"checks"`
	}

	t.Run("no_thresholds", func(t *testing.T) {
		violations := evaluateBenchmarkThresholds(benchmarkSummary{}, rawThresholds{})
		if len(violations) != 0 {
			t.Fatalf("expected no violations, got %v", violations)
		}
	})
	t.Run("p95_exceeded", func(t *testing.T) {
		p95 := 100.0
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{LatencyP95MS: 150},
			rawThresholds{P95MS: &p95},
		)
		if len(violations) != 1 || !strings.Contains(violations[0], "p95 exceeded") {
			t.Fatalf("expected p95 exceeded violation, got %v", violations)
		}
	})
	t.Run("p95_within_limit", func(t *testing.T) {
		p95 := 200.0
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{LatencyP95MS: 100},
			rawThresholds{P95MS: &p95},
		)
		if len(violations) != 0 {
			t.Fatalf("expected no violations, got %v", violations)
		}
	})
	t.Run("error_rate_exceeded", func(t *testing.T) {
		errRate := 0.05
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{ErrorRate: 0.06},
			rawThresholds{ErrorRate: &errRate},
		)
		if len(violations) != 1 || !strings.Contains(violations[0], "error rate exceeded") {
			t.Fatalf("expected error rate exceeded violation, got %v", violations)
		}
	})
	t.Run("error_rate_exactly_threshold", func(t *testing.T) {
		errRate := 0.05
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{ErrorRate: 0.05},
			rawThresholds{ErrorRate: &errRate},
		)
		// >= threshold triggers
		if len(violations) != 1 {
			t.Fatalf("expected 1 violation for exact threshold, got %v", violations)
		}
	})
	t.Run("checks_rate_below_threshold", func(t *testing.T) {
		checks := 0.95
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{ChecksRate: 0.90},
			rawThresholds{Checks: &checks},
		)
		if len(violations) != 1 || !strings.Contains(violations[0], "checks rate below threshold") {
			t.Fatalf("expected checks violation, got %v", violations)
		}
	})
	t.Run("all_violated_sorted", func(t *testing.T) {
		p95, errRate, checks := 100.0, 0.01, 0.99
		violations := evaluateBenchmarkThresholds(
			benchmarkSummary{LatencyP95MS: 200, ErrorRate: 0.1, ChecksRate: 0.5},
			rawThresholds{P95MS: &p95, ErrorRate: &errRate, Checks: &checks},
		)
		if len(violations) != 3 {
			t.Fatalf("expected 3 violations, got %d: %v", len(violations), violations)
		}
		// should be sorted alphabetically
		if violations[0] != "checks: checks rate below threshold" {
			t.Fatalf("expected checks violation first (sorted), got %q", violations[0])
		}
	})
}

// ---------------------------------------------------------------------------
// parseBenchmarkSummary — exercise the parser fully
// ---------------------------------------------------------------------------

func TestParseBenchmarkSummary(t *testing.T) {
	t.Run("valid_summary", func(t *testing.T) {
		raw := `{
			"metrics": {
				"http_req_duration": {"min":1,"avg":5,"med":4,"p(95)":10,"p(99)":15,"max":20},
				"http_reqs": {"count":100,"rate":10},
				"http_req_failed": {"rate":0.02,"fails":2,"passes":98},
				"checks": {"rate":0.98,"value":0},
				"bench_http_code_200": {"count":98,"rate":9.8},
				"bench_http_code_500": {"count":2,"rate":0.2}
			}
		}`
		summary, err := parseBenchmarkSummary([]byte(raw))
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if summary.Requests != 100 {
			t.Fatalf("expected 100 requests, got %d", summary.Requests)
		}
		if summary.FailedRequests != 2 {
			t.Fatalf("expected 2 failed, got %d", summary.FailedRequests)
		}
		if summary.LatencyP95MS != 10 {
			t.Fatalf("expected p95=10, got %v", summary.LatencyP95MS)
		}
		if len(summary.HTTPCodes) != 2 {
			t.Fatalf("expected 2 HTTP codes, got %v", summary.HTTPCodes)
		}
	})
	t.Run("checks_uses_value_fallback", func(t *testing.T) {
		raw := `{
			"metrics": {
				"http_req_duration": {},
				"http_reqs": {},
				"http_req_failed": {},
				"checks": {"value":0.99}
			}
		}`
		summary, err := parseBenchmarkSummary([]byte(raw))
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if summary.ChecksRate != 0.99 {
			t.Fatalf("expected checks rate 0.99 via value fallback, got %v", summary.ChecksRate)
		}
	})
	t.Run("failed_requests_computed_from_error_rate", func(t *testing.T) {
		raw := `{
			"metrics": {
				"http_req_duration": {},
				"http_reqs": {"count":200,"rate":20},
				"http_req_failed": {"rate":0.1,"fails":0},
				"checks": {}
			}
		}`
		summary, err := parseBenchmarkSummary([]byte(raw))
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		// fails=0 but errorRate>0 → failedRequests = round(200 * 0.1) = 20
		if summary.FailedRequests != 20 {
			t.Fatalf("expected 20 computed failed requests, got %d", summary.FailedRequests)
		}
	})
	t.Run("invalid_json", func(t *testing.T) {
		_, err := parseBenchmarkSummary([]byte("not json"))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
	t.Run("empty_metrics", func(t *testing.T) {
		_, err := parseBenchmarkSummary([]byte(`{"metrics":{}}`))
		if err == nil {
			t.Fatal("expected error for empty metrics")
		}
	})
}

// ---------------------------------------------------------------------------
// benchmarkStatusCodeMapToAny
// ---------------------------------------------------------------------------

func TestBenchmarkStatusCodeMapToAny(t *testing.T) {
	input := map[string]int{"200": 79, "500": 1}
	result := benchmarkStatusCodeMapToAny(input)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result["200"] != 79 {
		t.Fatalf("expected 200->79, got %v", result["200"])
	}
}

// ---------------------------------------------------------------------------
// resolveBenchmarkMethod — all branches
// ---------------------------------------------------------------------------

func TestResolveBenchmarkMethod(t *testing.T) {
	t.Run("default_GET", func(t *testing.T) {
		method, err := resolveBenchmarkMethod("", connectionProfile{})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if method != "GET" {
			t.Fatalf("expected GET, got %q", method)
		}
	})
	t.Run("normalizes_case", func(t *testing.T) {
		method, err := resolveBenchmarkMethod("post", connectionProfile{})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if method != "POST" {
			t.Fatalf("expected POST, got %q", method)
		}
	})
	t.Run("disallowed_method", func(t *testing.T) {
		_, err := resolveBenchmarkMethod("CONNECT", connectionProfile{})
		if err == nil {
			t.Fatal("expected error for disallowed method")
		}
	})
	t.Run("read_only_denies_post", func(t *testing.T) {
		_, err := resolveBenchmarkMethod("POST", connectionProfile{ReadOnly: true})
		if err == nil {
			t.Fatal("expected error for read_only POST")
		}
		if err.Code != app.ErrorCodePolicyDenied {
			t.Fatalf("expected policy_denied, got %s", err.Code)
		}
	})
	t.Run("read_only_allows_get", func(t *testing.T) {
		method, err := resolveBenchmarkMethod("GET", connectionProfile{ReadOnly: true})
		if err != nil {
			t.Fatalf(testUnexpectedErrorFmt, err)
		}
		if method != "GET" {
			t.Fatalf("expected GET, got %q", method)
		}
	})
}

// ---------------------------------------------------------------------------
// ensureBenchmarkWorkspace — local path (non-k8s)
// ---------------------------------------------------------------------------

func TestEnsureBenchmarkWorkspace_Local(t *testing.T) {
	workspace := t.TempDir()
	session := domain.Session{WorkspacePath: workspace}
	err := ensureBenchmarkWorkspace(context.Background(), nil, session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	benchDir := filepath.Join(workspace, ".bench")
	info, statErr := os.Stat(benchDir)
	if statErr != nil {
		t.Fatalf("expected .bench directory to exist: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatal("expected .bench to be a directory")
	}
}

// ---------------------------------------------------------------------------
// buildK6BenchmarkScript — verifies the script template output
// ---------------------------------------------------------------------------

func TestBuildK6BenchmarkScript(t *testing.T) {
	script, err := buildK6BenchmarkScript(
		"POST",
		"http://example.com/api",
		map[string]string{"content-type": "application/json"},
		`{"key":"value"}`,
		benchmarkLoadSpec{Mode: "constant_vus", VUs: 2, DurationMS: 5000},
		map[string][]string{"http_req_duration": {"p(95)<300"}},
	)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	s := string(script)
	if !strings.Contains(s, "k6/http") {
		t.Fatal("expected k6/http import in script")
	}
	if !strings.Contains(s, "POST") {
		t.Fatal("expected POST method in script")
	}
	if !strings.Contains(s, "example.com") {
		t.Fatal("expected target URL in script")
	}
	if !strings.Contains(s, "constant-vus") {
		t.Fatal("expected constant-vus scenario in script")
	}
}
