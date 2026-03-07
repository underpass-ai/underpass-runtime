package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
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

			summary := `{
  "metrics": {
    "http_req_duration": {
      "min": 2.1,
      "avg": 10.4,
      "med": 9.0,
      "p(95)": 20.7,
      "p(99)": 31.2,
      "max": 40.1,
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
		t.Fatalf("expected map output, got %T", result.Output)
	}
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
	if !strings.Contains(err.Message, "constraints violation") {
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
	spec, err := normalizeArrivalRateLoad("arrival_rate", 5000, 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Mode != "arrival_rate" {
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
	_, err := normalizeArrivalRateLoad("arrival_rate", 5000, 0, 0)
	if err == nil {
		t.Fatal("expected error for rps < 1")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestNormalizeArrivalRateLoad_RPSTooHigh(t *testing.T) {
	_, err := normalizeArrivalRateLoad("arrival_rate", 5000, benchmarkMaxRPS+1, 0)
	if err == nil {
		t.Fatal("expected error for rps > max")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
	if !strings.Contains(err.Message, "constraints violation") {
		t.Fatalf("expected constraints violation message, got %q", err.Message)
	}
}

func TestNormalizeArrivalRateLoad_VUSTooHigh(t *testing.T) {
	_, err := normalizeArrivalRateLoad("arrival_rate", 5000, 10, benchmarkMaxVUs+1)
	if err == nil {
		t.Fatal("expected error for vus > max")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
	if !strings.Contains(err.Message, "constraints violation") {
		t.Fatalf("expected constraints violation message, got %q", err.Message)
	}
}

func TestNormalizeArrivalRateLoad_ExplicitVUs(t *testing.T) {
	// valid with explicit vus=20 -> vus=20
	spec, err := normalizeArrivalRateLoad("arrival_rate", 5000, 10, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.VUs != 20 {
		t.Fatalf("expected VUs=20, got %d", spec.VUs)
	}
	if spec.RPS != 10 {
		t.Fatalf("expected RPS=10, got %d", spec.RPS)
	}
}
