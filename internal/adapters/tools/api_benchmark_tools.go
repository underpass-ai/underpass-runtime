package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	benchmarkDefaultDurationMS = 10000
	benchmarkDefaultVUs        = 1
	benchmarkMaxDurationMS     = 60000
	benchmarkMaxVUs            = 50
	benchmarkMaxRPS            = 200
	benchmarkMaxBodyBytes      = 32 * 1024
	benchmarkMaxHeaderBytes    = 8 * 1024
	benchmarkMaxHeaders        = 32
	benchmarkSummaryPath       = ".bench/summary.json"
	benchmarkRawMetricsPath    = ".bench/raw_metrics.json"
	benchmarkCommandMaxOutput  = 2 * 1024 * 1024
	benchmarkSummaryMaxBytes   = 2 * 1024 * 1024
	benchmarkRawMetricsMaxSize = 2 * 1024 * 1024
)

var (
	benchmarkSafeMethods = map[string]bool{
		"GET":     true,
		"HEAD":    true,
		"OPTIONS": true,
	}
	benchmarkAllowedMethods = map[string]bool{
		"GET":     true,
		"HEAD":    true,
		"OPTIONS": true,
		"POST":    true,
		"PUT":     true,
		"PATCH":   true,
		"DELETE":  true,
	}
	benchmarkDeniedHeaders = map[string]bool{
		"authorization":       true,
		"cookie":              true,
		"set-cookie":          true,
		"proxy-authorization": true,
		"x-api-key":           true,
		"x-auth-token":        true,
	}
	benchmarkAllowedHeaders = map[string]bool{
		"accept":           true,
		"accept-language":  true,
		"content-type":     true,
		"user-agent":       true,
		"x-request-id":     true,
		"x-correlation-id": true,
	}
)

type APIBenchmarkHandler struct {
	runner app.CommandRunner
}

type benchmarkRequest struct {
	ProfileID string `json:"profile_id"`
	Request   struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"request"`
	Load struct {
		Mode       string `json:"mode"`
		DurationMS int    `json:"duration_ms"`
		VUs        int    `json:"vus"`
		RPS        int    `json:"rps"`
	} `json:"load"`
	Thresholds struct {
		P95MS     *float64 `json:"p95_ms"`
		ErrorRate *float64 `json:"error_rate"`
		Checks    *float64 `json:"checks"`
	} `json:"thresholds"`
	IncludeRawMetrics bool `json:"include_raw_metrics"`
}

type benchmarkLoadSpec struct {
	Mode       string
	DurationMS int
	VUs        int
	RPS        int
}

type benchmarkSummary struct {
	LatencyMinMS float64
	LatencyAvgMS float64
	LatencyP50MS float64
	LatencyP95MS float64
	LatencyP99MS float64
	LatencyMaxMS float64

	RPSObserved    float64
	Requests       int
	FailedRequests int
	ErrorRate      float64
	ChecksRate     float64

	HTTPCodes map[string]int
}

func NewAPIBenchmarkHandler(runner app.CommandRunner) *APIBenchmarkHandler {
	return &APIBenchmarkHandler{runner: runner}
}

func (h *APIBenchmarkHandler) Name() string {
	return "api.benchmark"
}

// benchmarkPreparedRequest holds the validated and normalized fields extracted
// from the raw benchmark request, keeping the Invoke method free of validation
// complexity.
type benchmarkPreparedRequest struct {
	profileID    string
	targetURL    string
	method       string
	relativePath string
	headerCount  int
	headersBytes int
	bodyBytes    int
	loadSpec     benchmarkLoadSpec
	scriptBytes  []byte
}

// parseBenchmarkRequest unmarshals, validates, and normalizes the raw
// benchmark request into a ready-to-use benchmarkPreparedRequest.
func parseBenchmarkRequest(session domain.Session, args json.RawMessage) (benchmarkPreparedRequest, benchmarkRequest, *domain.Error) {
	request := benchmarkRequest{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return benchmarkPreparedRequest{}, request, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid api.benchmark args",
				Retryable: false,
			}
		}
	}

	profile, endpoint, profileErr := resolveAPIBenchmarkProfile(session, request.ProfileID)
	if profileErr != nil {
		return benchmarkPreparedRequest{}, request, profileErr
	}

	method, methodErr := resolveBenchmarkMethod(request.Request.Method, profile)
	if methodErr != nil {
		return benchmarkPreparedRequest{}, request, methodErr
	}

	relativePath, pathOnly, pathErr := normalizeBenchmarkPath(request.Request.Path)
	if pathErr != nil {
		return benchmarkPreparedRequest{}, request, benchmarkInvalidArgument(pathErr.Error())
	}
	if !routeAllowedByProfile(pathOnly, profile) {
		return benchmarkPreparedRequest{}, request, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "route outside profile allowlist",
			Retryable: false,
		}
	}

	headers, headersBytes, headersErr := sanitizeBenchmarkHeaders(request.Request.Headers)
	if headersErr != nil {
		return benchmarkPreparedRequest{}, request, headersErr
	}

	body := request.Request.Body
	bodyBytes := len([]byte(body))
	if bodyBytes > benchmarkMaxBodyBytes {
		return benchmarkPreparedRequest{}, request, benchmarkConstraintViolation(
			fmt.Sprintf("request.body exceeds %d bytes", benchmarkMaxBodyBytes),
		)
	}

	loadSpec, loadErr := normalizeBenchmarkLoad(request.Load)
	if loadErr != nil {
		return benchmarkPreparedRequest{}, request, loadErr
	}

	thresholds, thresholdErr := normalizeBenchmarkThresholds(request.Thresholds)
	if thresholdErr != nil {
		return benchmarkPreparedRequest{}, request, thresholdErr
	}

	targetURL, urlErr := buildBenchmarkTargetURL(endpoint, relativePath)
	if urlErr != nil {
		return benchmarkPreparedRequest{}, request, benchmarkInvalidArgument(urlErr.Error())
	}

	scriptBytes, scriptErr := buildK6BenchmarkScript(method, targetURL, headers, body, loadSpec, thresholds)
	if scriptErr != nil {
		return benchmarkPreparedRequest{}, request, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   scriptErr.Error(),
			Retryable: false,
		}
	}

	return benchmarkPreparedRequest{
		profileID:    profile.ID,
		targetURL:    targetURL,
		method:       method,
		relativePath: relativePath,
		headerCount:  len(headers),
		headersBytes: headersBytes,
		bodyBytes:    bodyBytes,
		loadSpec:     loadSpec,
		scriptBytes:  scriptBytes,
	}, request, nil
}

func (h *APIBenchmarkHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	prepared, request, prepErr := parseBenchmarkRequest(session, args)
	if prepErr != nil {
		return app.ToolRunResult{}, prepErr
	}

	if err := ensureBenchmarkWorkspace(ctx, ensureRunner(h.runner), session); err != nil {
		return app.ToolRunResult{}, err
	}

	commandArgs := []string{"run", "--summary-export", benchmarkSummaryPath}
	if request.IncludeRawMetrics {
		commandArgs = append(commandArgs, "--out", "json="+benchmarkRawMetricsPath)
	}
	commandArgs = append(commandArgs, "-")

	commandResult, runErr := ensureRunner(h.runner).Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "k6",
		Args:     commandArgs,
		Stdin:    prepared.scriptBytes,
		MaxBytes: benchmarkCommandMaxOutput,
	})
	redactedLog := redactBenchmarkText(commandResult.Output)

	artifacts := buildBenchmarkBaseArtifacts(prepared.scriptBytes, redactedLog)

	if runErr != nil {
		return buildBenchmarkFailedResult(commandResult.ExitCode, prepared.profileID, prepared.targetURL, redactedLog, artifacts), toToolError(runErr, redactedLog)
	}

	summaryBytes, _, summaryReadErr := readWorkspaceFile(
		ctx,
		ensureRunner(h.runner),
		session,
		benchmarkSummaryPath,
		benchmarkSummaryMaxBytes,
	)
	if summaryReadErr != nil {
		return app.ToolRunResult{Artifacts: artifacts}, summaryReadErr
	}
	artifacts = append(artifacts, app.ArtifactPayload{
		Name:        "benchmark-summary.json",
		ContentType: "application/json",
		Data:        summaryBytes,
	})

	rawMetricsAttached, rawMetricsTruncated, artifacts := attachBenchmarkRawMetrics(ctx, ensureRunner(h.runner), session, request.IncludeRawMetrics, artifacts)

	parsed, parseErr := parseBenchmarkSummary(summaryBytes)
	if parseErr != nil {
		return app.ToolRunResult{
				ExitCode:  commandResult.ExitCode,
				Artifacts: artifacts,
				Logs: []domain.LogLine{{
					At:      time.Now().UTC(),
					Channel: "stdout",
					Message: "api benchmark completed but summary parse failed",
				}},
			}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   fmt.Sprintf("failed to parse k6 summary: %v", parseErr),
				Retryable: false,
			}
	}
	thresholdViolations := evaluateBenchmarkThresholds(parsed, request.Thresholds)

	output := buildBenchmarkSuccessOutput(benchmarkSuccessInput{
		profileID:           prepared.profileID,
		targetURL:           prepared.targetURL,
		method:              prepared.method,
		relativePath:        prepared.relativePath,
		headerCount:         prepared.headerCount,
		headersBytes:        prepared.headersBytes,
		bodyBytes:           prepared.bodyBytes,
		loadSpec:            prepared.loadSpec,
		parsed:              parsed,
		thresholdViolations: thresholdViolations,
		exitCode:            commandResult.ExitCode,
	})
	if request.IncludeRawMetrics {
		output["artifacts"].(map[string]any)["raw_metrics_json"] = "benchmark-raw-metrics.json"
		output["raw_metrics_attached"] = rawMetricsAttached
		output["raw_metrics_truncated"] = rawMetricsTruncated
	}

	return app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "api benchmark completed",
		}},
		Output:    output,
		Artifacts: artifacts,
	}, nil
}

// resolveBenchmarkMethod normalises and validates the HTTP method from the
// request, enforcing the profile's read-only constraint.
func resolveBenchmarkMethod(raw string, profile connectionProfile) (string, *domain.Error) {
	method := strings.ToUpper(strings.TrimSpace(raw))
	if method == "" {
		method = "GET"
	}
	if !benchmarkAllowedMethods[method] {
		return "", benchmarkInvalidArgument("request.method is not allowed")
	}
	if profile.ReadOnly && !benchmarkSafeMethods[method] {
		return "", &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "profile is read_only",
			Retryable: false,
		}
	}
	return method, nil
}

// buildBenchmarkBaseArtifacts returns the two artifacts that are always
// attached to a benchmark result (k6 script + run log).
func buildBenchmarkBaseArtifacts(scriptBytes []byte, redactedLog string) []app.ArtifactPayload {
	return []app.ArtifactPayload{
		{
			Name:        "benchmark-k6.js",
			ContentType: "application/javascript",
			Data:        scriptBytes,
		},
		{
			Name:        "benchmark-k6.log",
			ContentType: "text/plain",
			Data:        []byte(redactedLog),
		},
	}
}

// buildBenchmarkFailedResult constructs the ToolRunResult for a k6 run that
// returned a non-zero exit code.
func buildBenchmarkFailedResult(exitCode int, profileID, targetURL, redactedLog string, artifacts []app.ArtifactPayload) app.ToolRunResult {
	return app.ToolRunResult{
		ExitCode: exitCode,
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stderr",
			Message: redactedLog,
		}},
		Output: map[string]any{
			"profile_id": profileID,
			"target_url": redactBenchmarkText(targetURL),
			"exit_code":  exitCode,
			"status":     sweStepFailed,
			"output":     redactedLog,
		},
		Artifacts: artifacts,
	}
}

// benchmarkSuccessInput groups the parameters for building a successful
// benchmark output map.
type benchmarkSuccessInput struct {
	profileID           string
	targetURL           string
	method              string
	relativePath        string
	headerCount         int
	headersBytes        int
	bodyBytes           int
	loadSpec            benchmarkLoadSpec
	parsed              benchmarkSummary
	thresholdViolations []string
	exitCode            int
}

// buildBenchmarkSuccessOutput assembles the output map for a successful
// benchmark run, keeping the Invoke function free of large inline literals.
func buildBenchmarkSuccessOutput(in benchmarkSuccessInput) map[string]any {
	return map[string]any{
		"profile_id": in.profileID,
		"target_url": redactBenchmarkText(in.targetURL),
		"request": map[string]any{
			"method":       in.method,
			"path":         in.relativePath,
			"header_count": in.headerCount,
			"header_bytes": in.headersBytes,
			"body_bytes":   in.bodyBytes,
		},
		"load": map[string]any{
			"mode":        in.loadSpec.Mode,
			"duration_ms": in.loadSpec.DurationMS,
			"vus":         in.loadSpec.VUs,
			"rps":         in.loadSpec.RPS,
		},
		"latency_ms": map[string]any{
			"min": in.parsed.LatencyMinMS,
			"avg": in.parsed.LatencyAvgMS,
			"p50": in.parsed.LatencyP50MS,
			"p95": in.parsed.LatencyP95MS,
			"p99": in.parsed.LatencyP99MS,
			"max": in.parsed.LatencyMaxMS,
		},
		"rps_observed":    in.parsed.RPSObserved,
		"requests":        in.parsed.Requests,
		"failed_requests": in.parsed.FailedRequests,
		"error_rate":      in.parsed.ErrorRate,
		"http_codes":      benchmarkStatusCodeMapToAny(in.parsed.HTTPCodes),
		"thresholds": map[string]any{
			"passed":     len(in.thresholdViolations) == 0,
			"violations": in.thresholdViolations,
		},
		"artifacts": map[string]any{
			"summary_json": "benchmark-summary.json",
			"k6_script":    "benchmark-k6.js",
			"k6_log":       "benchmark-k6.log",
		},
		"exit_code": in.exitCode,
		"status":    sweStepSucceeded,
	}
}

func attachBenchmarkRawMetrics(ctx context.Context, runner app.CommandRunner, session domain.Session, include bool, artifacts []app.ArtifactPayload) (attached bool, truncated bool, updated []app.ArtifactPayload) {
	if !include {
		return false, false, artifacts
	}
	rawMetricsBytes, isTruncated, rawErr := readWorkspaceFile(ctx, runner, session, benchmarkRawMetricsPath, benchmarkRawMetricsMaxSize)
	if rawErr != nil {
		return false, false, artifacts
	}
	artifacts = append(artifacts, app.ArtifactPayload{
		Name:        "benchmark-raw-metrics.json",
		ContentType: "application/json",
		Data:        rawMetricsBytes,
	})
	return true, isTruncated, artifacts
}

func resolveAPIBenchmarkProfile(session domain.Session, requestedProfileID string) (connectionProfile, string, *domain.Error) {
	return resolveTypedProfile(session, requestedProfileID,
		[]string{"http", "api"}, "", "")
}

func normalizeBenchmarkPath(raw string) (string, string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", "", fmt.Errorf("request.path is required")
	}
	if strings.Contains(candidate, "\n") || strings.Contains(candidate, "\r") {
		return "", "", fmt.Errorf("request.path contains invalid characters")
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", "", fmt.Errorf("request.path is invalid")
	}
	if parsed.IsAbs() || strings.TrimSpace(parsed.Host) != "" {
		return "", "", fmt.Errorf("request.path must be relative")
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return "", "", fmt.Errorf("request.path must start with '/'")
	}
	if strings.Contains(parsed.Path, "..") {
		return "", "", fmt.Errorf("request.path must not contain '..'")
	}

	relativePath := parsed.Path
	if parsed.RawQuery != "" {
		relativePath += "?" + parsed.RawQuery
	}
	return relativePath, parsed.Path, nil
}

func sanitizeBenchmarkHeaders(raw map[string]string) (map[string]string, int, *domain.Error) {
	if len(raw) == 0 {
		return map[string]string{}, 0, nil
	}
	if len(raw) > benchmarkMaxHeaders {
		return nil, 0, benchmarkConstraintViolation(
			fmt.Sprintf("request.headers exceeds %d headers", benchmarkMaxHeaders),
		)
	}

	sanitized := make(map[string]string, len(raw))
	totalBytes := 0
	for key, value := range raw {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		if err := validateSingleBenchmarkHeader(name, value); err != nil {
			return nil, 0, err
		}
		totalBytes += len(name) + len(value)
		sanitized[name] = value
	}

	if totalBytes > benchmarkMaxHeaderBytes {
		return nil, 0, benchmarkConstraintViolation(
			fmt.Sprintf("request.headers exceeds %d bytes", benchmarkMaxHeaderBytes),
		)
	}
	return sanitized, totalBytes, nil
}

func validateSingleBenchmarkHeader(name, value string) *domain.Error {
	lower := strings.ToLower(name)
	if benchmarkDeniedHeaders[lower] {
		return benchmarkInvalidArgument(fmt.Sprintf("header %s is not allowed", lower))
	}
	if !benchmarkAllowedHeaders[lower] && !strings.HasPrefix(lower, "x-") {
		return benchmarkInvalidArgument(fmt.Sprintf("header %s is not allowed", lower))
	}
	if strings.Contains(name, "\n") || strings.Contains(name, "\r") {
		return benchmarkInvalidArgument("header name contains invalid characters")
	}
	if strings.Contains(value, "\n") || strings.Contains(value, "\r") {
		return benchmarkInvalidArgument("header value contains invalid characters")
	}
	return nil
}

func normalizeBenchmarkLoad(raw struct {
	Mode       string `json:"mode"`
	DurationMS int    `json:"duration_ms"`
	VUs        int    `json:"vus"`
	RPS        int    `json:"rps"`
}) (benchmarkLoadSpec, *domain.Error) {
	mode := strings.ToLower(strings.TrimSpace(raw.Mode))
	if mode == "" {
		mode = "constant_vus"
	}
	if mode != "constant_vus" && mode != "arrival_rate" {
		return benchmarkLoadSpec{}, benchmarkInvalidArgument("load.mode must be constant_vus or arrival_rate")
	}

	durationMS := raw.DurationMS
	if durationMS == 0 {
		durationMS = benchmarkDefaultDurationMS
	}
	if durationMS < 100 {
		return benchmarkLoadSpec{}, benchmarkConstraintViolation("load.duration_ms must be >= 100")
	}
	if durationMS > benchmarkMaxDurationMS {
		return benchmarkLoadSpec{}, benchmarkConstraintViolation(
			fmt.Sprintf("load.duration_ms exceeds %d", benchmarkMaxDurationMS),
		)
	}

	if mode == "constant_vus" {
		vus := raw.VUs
		if vus == 0 {
			vus = benchmarkDefaultVUs
		}
		if vus < 1 {
			return benchmarkLoadSpec{}, benchmarkConstraintViolation("load.vus must be >= 1")
		}
		if vus > benchmarkMaxVUs {
			return benchmarkLoadSpec{}, benchmarkConstraintViolation(
				fmt.Sprintf("load.vus exceeds %d", benchmarkMaxVUs),
			)
		}
		return benchmarkLoadSpec{
			Mode:       mode,
			DurationMS: durationMS,
			VUs:        vus,
			RPS:        0,
		}, nil
	}

	return normalizeArrivalRateLoad(mode, durationMS, raw.RPS, raw.VUs)
}

func normalizeArrivalRateLoad(mode string, durationMS, rawRPS, rawVUs int) (benchmarkLoadSpec, *domain.Error) {
	rps := rawRPS
	if rps < 1 {
		return benchmarkLoadSpec{}, benchmarkConstraintViolation("load.rps must be >= 1 for arrival_rate mode")
	}
	if rps > benchmarkMaxRPS {
		return benchmarkLoadSpec{}, benchmarkConstraintViolation(
			fmt.Sprintf("load.rps exceeds %d", benchmarkMaxRPS),
		)
	}
	vus := rawVUs
	if vus == 0 {
		vus = rps
	}
	if vus < 1 {
		vus = 1
	}
	if vus > benchmarkMaxVUs {
		return benchmarkLoadSpec{}, benchmarkConstraintViolation(
			fmt.Sprintf("load.vus exceeds %d", benchmarkMaxVUs),
		)
	}
	return benchmarkLoadSpec{
		Mode:       mode,
		DurationMS: durationMS,
		VUs:        vus,
		RPS:        rps,
	}, nil
}

func normalizeBenchmarkThresholds(raw struct {
	P95MS     *float64 `json:"p95_ms"`
	ErrorRate *float64 `json:"error_rate"`
	Checks    *float64 `json:"checks"`
}) (map[string][]string, *domain.Error) {
	thresholds := map[string][]string{}

	if raw.P95MS != nil {
		if *raw.P95MS <= 0 {
			return nil, benchmarkInvalidArgument("thresholds.p95_ms must be > 0")
		}
		thresholds["http_req_duration"] = []string{
			"p(95)<" + strconv.FormatFloat(*raw.P95MS, 'f', -1, 64),
		}
	}

	if raw.ErrorRate != nil {
		if *raw.ErrorRate < 0 || *raw.ErrorRate > 1 {
			return nil, benchmarkInvalidArgument("thresholds.error_rate must be between 0 and 1")
		}
		thresholds["http_req_failed"] = []string{
			"rate<" + strconv.FormatFloat(*raw.ErrorRate, 'f', -1, 64),
		}
	}

	if raw.Checks != nil {
		if *raw.Checks < 0 || *raw.Checks > 1 {
			return nil, benchmarkInvalidArgument("thresholds.checks must be between 0 and 1")
		}
		thresholds["checks"] = []string{
			"rate>" + strconv.FormatFloat(*raw.Checks, 'f', -1, 64),
		}
	}

	return thresholds, nil
}

func buildBenchmarkTargetURL(endpoint, relativePath string) (string, error) {
	baseURL := strings.TrimSpace(endpoint)
	if baseURL == "" {
		return "", fmt.Errorf("benchmark profile endpoint not configured")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("benchmark endpoint is invalid")
	}
	if parsedBase.Scheme != "http" && parsedBase.Scheme != "https" {
		return "", fmt.Errorf("benchmark endpoint must use http or https")
	}
	if strings.TrimSpace(parsedBase.Host) == "" {
		return "", fmt.Errorf("benchmark endpoint host is required")
	}

	parsedPath, err := url.Parse(relativePath)
	if err != nil {
		return "", fmt.Errorf("request.path is invalid")
	}
	if parsedPath.Path == "" {
		return "", fmt.Errorf("request.path is required")
	}

	basePath := strings.TrimSuffix(parsedBase.Path, "/")
	finalPath := parsedPath.Path
	if basePath != "" && basePath != "/" {
		finalPath = basePath + parsedPath.Path
	}

	target := &url.URL{
		Scheme:   parsedBase.Scheme,
		Host:     parsedBase.Host,
		Path:     finalPath,
		RawQuery: parsedPath.RawQuery,
	}
	return target.String(), nil
}

func buildK6BenchmarkScript(
	method string,
	targetURL string,
	headers map[string]string,
	body string,
	load benchmarkLoadSpec,
	thresholds map[string][]string,
) ([]byte, error) {
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return nil, err
	}
	thresholdsJSON, err := json.Marshal(thresholds)
	if err != nil {
		return nil, err
	}
	methodJSON, err := json.Marshal(method)
	if err != nil {
		return nil, err
	}
	urlJSON, err := json.Marshal(targetURL)
	if err != nil {
		return nil, err
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	scenario, err := k6ScenarioJSON(load)
	if err != nil {
		return nil, err
	}

	script := fmt.Sprintf(`import http from "k6/http";
import { check } from "k6";
import { Counter } from "k6/metrics";

const METHOD = %s;
const TARGET_URL = %s;
const HEADERS = %s;
const BODY = %s;
const CODE_COUNTERS = {};

for (let code = 100; code <= 599; code++) {
  CODE_COUNTERS[code] = new Counter("bench_http_code_" + code);
}

export const options = {
  scenarios: { benchmark: %s },
  thresholds: %s,
};

export default function () {
  const requestBody = (METHOD === "GET" || METHOD === "HEAD" || METHOD === "OPTIONS") ? null : BODY;
  const response = http.request(METHOD, TARGET_URL, requestBody, { headers: HEADERS });
  const status = response && response.status ? Number(response.status) : 0;
  if (status >= 100 && status <= 599 && CODE_COUNTERS[status]) {
    CODE_COUNTERS[status].add(1);
  }
  check(response, {
    "status < 500": (r) => !!r && r.status < 500,
  });
}
`, string(methodJSON), string(urlJSON), string(headersJSON), string(bodyJSON), scenario, string(thresholdsJSON))
	return []byte(script), nil
}

func k6ScenarioJSON(load benchmarkLoadSpec) (string, error) {
	if load.Mode == "constant_vus" {
		scenario := map[string]any{
			"executor": "constant-vus",
			"vus":      load.VUs,
			"duration": fmt.Sprintf("%dms", load.DurationMS),
		}
		data, err := json.Marshal(scenario)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	preAllocated := load.VUs
	if preAllocated < 1 {
		preAllocated = 1
	}
	if preAllocated > benchmarkMaxVUs {
		preAllocated = benchmarkMaxVUs
	}

	scenario := map[string]any{
		"executor":        "constant-arrival-rate",
		"rate":            load.RPS,
		"timeUnit":        "1s",
		"duration":        fmt.Sprintf("%dms", load.DurationMS),
		"preAllocatedVUs": preAllocated,
		"maxVUs":          benchmarkMaxVUs,
	}
	data, err := json.Marshal(scenario)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ensureBenchmarkWorkspace(ctx context.Context, runner app.CommandRunner, session domain.Session) *domain.Error {
	if isKubernetesRuntime(session) {
		result, err := runShellCommand(ctx, runner, session, "mkdir -p .bench", nil, 64*1024)
		if err != nil {
			return toFSRunnerError(err, result.Output)
		}
		return nil
	}

	resolved := filepath.Join(session.WorkspacePath, ".bench")
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("failed to create benchmark workspace: %v", err),
			Retryable: false,
		}
	}
	return nil
}

func parseBenchmarkSummary(raw []byte) (benchmarkSummary, error) {
	var payload struct {
		Metrics map[string]map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return benchmarkSummary{}, err
	}
	if len(payload.Metrics) == 0 {
		return benchmarkSummary{}, fmt.Errorf("k6 summary missing metrics")
	}

	durationMetric := payload.Metrics["http_req_duration"]
	requestsMetric := payload.Metrics["http_reqs"]
	failedMetric := payload.Metrics["http_req_failed"]
	checksMetric := payload.Metrics["checks"]

	requests := metricInt(requestsMetric, "count")
	rpsObserved := metricFloat(requestsMetric, "rate")
	errorRate := metricFloat(failedMetric, "rate")
	checksRate := metricFloat(checksMetric, "rate")
	if checksRate == 0 {
		checksRate = metricFloat(checksMetric, "value")
	}

	failedRequests := metricInt(failedMetric, "fails")
	if failedRequests == 0 && requests > 0 && errorRate > 0 {
		failedRequests = int(math.Round(float64(requests) * errorRate))
	}

	httpCodes := map[string]int{}
	for metricName, metricValue := range payload.Metrics {
		if !strings.HasPrefix(metricName, "bench_http_code_") {
			continue
		}
		code := strings.TrimPrefix(metricName, "bench_http_code_")
		count := metricInt(metricValue, "count")
		if count > 0 {
			httpCodes[code] = count
		}
	}

	return benchmarkSummary{
		LatencyMinMS: metricFloat(durationMetric, "min"),
		LatencyAvgMS: metricFloat(durationMetric, "avg"),
		LatencyP50MS: metricFloat(durationMetric, "med"),
		LatencyP95MS: metricFloat(durationMetric, "p(95)"),
		LatencyP99MS: metricFloat(durationMetric, "p(99)"),
		LatencyMaxMS: metricFloat(durationMetric, "max"),

		RPSObserved:    rpsObserved,
		Requests:       requests,
		FailedRequests: failedRequests,
		ErrorRate:      errorRate,
		ChecksRate:     checksRate,
		HTTPCodes:      httpCodes,
	}, nil
}

func metricFloat(metric map[string]any, key string) float64 {
	if len(metric) == 0 {
		return 0
	}
	if values, ok := metric["values"].(map[string]any); ok {
		if parsed, found := asFloat(values[key]); found {
			return parsed
		}
	}
	parsed, _ := asFloat(metric[key])
	return parsed
}

func metricInt(metric map[string]any, key string) int {
	return int(math.Round(metricFloat(metric, key)))
}

func asFloat(raw any) (float64, bool) {
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func routeAllowedByProfile(path string, profile connectionProfile) bool {
	patterns := extractBenchmarkProfileStringList(profile.Scopes, "routes")
	if len(patterns) == 0 {
		patterns = extractBenchmarkProfileStringList(profile.Scopes, "paths")
	}
	if len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		if routePatternMatch(pattern, path) {
			return true
		}
	}
	return false
}

func routePatternMatch(pattern, path string) bool {
	trimmedPattern := strings.TrimSpace(pattern)
	trimmedPath := strings.TrimSpace(path)
	if trimmedPattern == "" || trimmedPath == "" {
		return false
	}
	if trimmedPattern == "*" || trimmedPattern == trimmedPath {
		return true
	}
	if strings.HasPrefix(trimmedPattern, "regex:") {
		rawRegex := strings.TrimSpace(strings.TrimPrefix(trimmedPattern, "regex:"))
		if rawRegex == "" {
			return false
		}
		compiled, err := regexp.Compile(rawRegex)
		return err == nil && compiled.MatchString(trimmedPath)
	}
	if strings.Contains(trimmedPattern, "*") {
		regex := "^" + regexp.QuoteMeta(trimmedPattern) + "$"
		regex = strings.ReplaceAll(regex, `\*`, ".*")
		compiled, err := regexp.Compile(regex)
		return err == nil && compiled.MatchString(trimmedPath)
	}
	if strings.HasSuffix(trimmedPattern, "/") {
		return strings.HasPrefix(trimmedPath, trimmedPattern)
	}
	return false
}

func extractBenchmarkProfileStringList(scopes map[string]any, key string) []string {
	if len(scopes) == 0 {
		return nil
	}
	raw, found := scopes[key]
	if !found {
		return nil
	}

	switch typed := raw.(type) {
	case []string:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			candidate := strings.TrimSpace(entry)
			if candidate == "" {
				continue
			}
			values = append(values, candidate)
		}
		return values
	case []any:
		return extractStringListFromAnySlice(typed)
	default:
		return nil
	}
}

func extractStringListFromAnySlice(typed []any) []string {
	values := make([]string, 0, len(typed))
	for _, entry := range typed {
		if s, ok := entry.(string); ok {
			if candidate := strings.TrimSpace(s); candidate != "" {
				values = append(values, candidate)
			}
		}
	}
	return values
}

func redactBenchmarkText(raw string) string {
	return redactSensitiveText(raw)
}

func benchmarkInvalidArgument(message string) *domain.Error {
	return &domain.Error{
		Code:      app.ErrorCodeInvalidArgument,
		Message:   message,
		Retryable: false,
	}
}

func benchmarkConstraintViolation(message string) *domain.Error {
	return benchmarkInvalidArgument("constraints violation: " + message)
}

func benchmarkStatusCodeMapToAny(raw map[string]int) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func evaluateBenchmarkThresholds(
	summary benchmarkSummary,
	raw struct {
		P95MS     *float64 `json:"p95_ms"`
		ErrorRate *float64 `json:"error_rate"`
		Checks    *float64 `json:"checks"`
	},
) []string {
	violations := make([]string, 0, 3)
	if raw.P95MS != nil && summary.LatencyP95MS > *raw.P95MS {
		violations = append(violations, "http_req_duration: p95 exceeded")
	}
	if raw.ErrorRate != nil && summary.ErrorRate >= *raw.ErrorRate {
		violations = append(violations, "http_req_failed: error rate exceeded")
	}
	if raw.Checks != nil && summary.ChecksRate <= *raw.Checks {
		violations = append(violations, "checks: checks rate below threshold")
	}
	sort.Strings(violations)
	return violations
}
