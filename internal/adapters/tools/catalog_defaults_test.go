package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestDefaultCapabilities_Metadata(t *testing.T) {
	capabilities := DefaultCapabilities()
	if len(capabilities) == 0 {
		t.Fatal("expected default capabilities")
	}

	seen := map[string]bool{}
	pathPolicyRequired := map[string]bool{
		"fs.list":                    true,
		"fs.read_file":               true,
		"fs.write_file":              true,
		"fs.mkdir":                   true,
		"fs.move":                    true,
		"fs.copy":                    true,
		"fs.delete":                  true,
		"fs.stat":                    true,
		"fs.search":                  true,
		"git.diff":                   true,
		"artifact.upload":            true,
		"artifact.download":          true,
		"artifact.list":              true,
		"image.build":                true,
		"image.inspect":              true,
		"security.scan_dependencies": true,
		"sbom.generate":              true,
		"security.scan_secrets":      true,
		"security.scan_container":    true,
		"security.license_check":     true,
		"repo.changed_files":         true,
		"repo.symbol_search":         true,
		"repo.find_references":       true,
	}
	for _, capability := range capabilities {
		if capability.Name == "" {
			t.Fatal("capability name must not be empty")
		}
		if seen[capability.Name] {
			t.Fatalf("duplicate capability name: %s", capability.Name)
		}
		seen[capability.Name] = true
		if capability.Observability.TraceName == "" || capability.Observability.SpanName == "" {
			t.Fatalf("missing observability names for %s", capability.Name)
		}
		if len(capability.InputSchema) == 0 || len(capability.OutputSchema) == 0 {
			t.Fatalf("missing schemas for %s", capability.Name)
		}
		if pathPolicyRequired[capability.Name] && len(capability.Policy.PathFields) == 0 {
			t.Fatalf("missing explicit path policy fields for %s", capability.Name)
		}
	}

	requiredCapabilities := []string{
		"fs.write_file", "fs.mkdir", "fs.move", "fs.copy", "fs.delete", "fs.stat",
		"conn.list_profiles", "conn.describe_profile", "api.benchmark",
		"nats.request", "nats.publish", "nats.subscribe_pull",
		"kafka.consume", "kafka.produce", "kafka.topic_metadata", "notify.escalation_channel",
		"rabbit.consume", "rabbit.publish", "rabbit.queue_info",
		"redis.get", "redis.mget", "redis.scan", "redis.ttl", "redis.exists", "redis.set", "redis.del",
		"mongo.find", "mongo.aggregate",
		"git.status", "git.diff", "git.apply_patch", "git.checkout", "git.log", "git.show",
		"git.branch_list", "git.commit", "git.push", "git.fetch", "git.pull",
		"repo.detect_project_type", "repo.detect_toolchain", "repo.validate",
		"repo.build", "repo.test", "repo.run_tests", "repo.test_failures_summary",
		"repo.stacktrace_summary", "repo.changed_files", "repo.symbol_search",
		"repo.find_references", "repo.coverage_report", "repo.static_analysis", "repo.package",
		"artifact.upload", "artifact.download", "artifact.list",
		"image.build", "image.push", "image.inspect",
		"container.ps", "container.logs", "container.run", "container.exec",
		"k8s.get_pods", "k8s.get_services", "k8s.get_deployments", "k8s.get_replicasets", "k8s.get_images",
		"k8s.get_logs", "k8s.apply_manifest", "k8s.rollout_status", "k8s.rollout_pause", "k8s.rollout_undo", "k8s.restart_deployment",
		"k8s.scale_deployment", "k8s.restart_pods", "k8s.circuit_break",
		"security.scan_dependencies", "sbom.generate", "security.scan_secrets",
		"security.scan_container", "security.license_check",
		"quality.gate", "ci.run_pipeline",
		"go.mod.tidy", "go.build", "go.test", "rust.build", "node.typecheck",
		"python.validate", "c.build",
	}
	for _, name := range requiredCapabilities {
		if !seen[name] {
			t.Fatalf("expected critical capability missing: %s", name)
		}
	}
}

func TestLoadCatalog_InvalidYAML(t *testing.T) {
	_, err := loadCatalog([]byte("{{{{not yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "catalog_defaults.yaml") {
		t.Fatalf("error should mention catalog file, got: %s", err)
	}
}

func TestLoadCatalog_MissingName(t *testing.T) {
	yaml := `capabilities:
  - name: ""
    description: "test"
    input_schema: "{}"
    output_schema: "{}"
`
	_, err := loadCatalog([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "missing name") {
		t.Fatalf("error should mention missing name, got: %s", err)
	}
}

func TestLoadCatalog_EmptyInputSchema(t *testing.T) {
	yaml := `capabilities:
  - name: "test.tool"
    description: "test"
    input_schema: ""
    output_schema: "{}"
`
	_, err := loadCatalog([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for empty input_schema")
	}
	if !strings.Contains(err.Error(), "empty input_schema") {
		t.Fatalf("error should mention empty input_schema, got: %s", err)
	}
}

func TestLoadCatalog_EmptyOutputSchema(t *testing.T) {
	yaml := `capabilities:
  - name: "test.tool"
    description: "test"
    input_schema: "{}"
    output_schema: ""
`
	_, err := loadCatalog([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for empty output_schema")
	}
	if !strings.Contains(err.Error(), "empty output_schema") {
		t.Fatalf("error should mention empty output_schema, got: %s", err)
	}
}

func TestLoadCatalog_InvalidJSON(t *testing.T) {
	yaml := `capabilities:
  - name: "test.tool"
    description: "test"
    input_schema: "not json"
    output_schema: "{}"
`
	_, err := loadCatalog([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("error should mention invalid JSON, got: %s", err)
	}
}

func TestLoadCatalog_InvalidExampleJSON(t *testing.T) {
	yaml := `capabilities:
  - name: "test.tool"
    description: "test"
    input_schema: "{}"
    output_schema: "{}"
    examples:
      - "not json"
`
	_, err := loadCatalog([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid example JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON in examples[0]") {
		t.Fatalf("error should mention invalid example, got: %s", err)
	}
}

func TestLoadCatalog_ValidMinimal(t *testing.T) {
	yaml := `capabilities:
  - name: "test.tool"
    description: "a test tool"
    input_schema: '{"type":"object"}'
    output_schema: '{"type":"object"}'
    scope: session
    risk_level: low
`
	caps, err := loadCatalog([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(caps))
	}
	if caps[0].Name != "test.tool" {
		t.Fatalf("expected name test.tool, got %s", caps[0].Name)
	}
	if caps[0].Observability.TraceName != defaultTraceName {
		t.Fatalf("expected trace name %s, got %s", defaultTraceName, caps[0].Observability.TraceName)
	}
	if caps[0].Observability.SpanName != "test.tool" {
		t.Fatalf("expected span name test.tool, got %s", caps[0].Observability.SpanName)
	}
}

func TestValidJSON_Empty(t *testing.T) {
	_, err := validJSON("", "tool", "field")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestValidJSON_Invalid(t *testing.T) {
	_, err := validJSON("{broken", "tool", "field")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidJSON_Valid(t *testing.T) {
	raw, err := validJSON(`{"key":"value"}`, "tool", "field")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if string(raw) != `{"key":"value"}` {
		t.Fatalf("unexpected raw JSON: %s", string(raw))
	}
}

func TestSaturationNotifyCatalogContracts(t *testing.T) {
	capabilities := DefaultCapabilities()
	byName := make(map[string]domain.Capability, len(capabilities))
	for _, capability := range capabilities {
		byName[capability.Name] = capability
	}

	scale, ok := byName["k8s.scale_deployment"]
	if !ok {
		t.Fatal("expected k8s.scale_deployment capability")
	}
	if scale.Idempotency != domain.IdempotencyBestEffort {
		t.Fatalf("expected k8s.scale_deployment idempotency=best-effort, got %s", scale.Idempotency)
	}
	if !strings.Contains(scale.Description, "delta path is best-effort") {
		t.Fatalf("expected scale deployment description to explain delta semantics, got %q", scale.Description)
	}

	restart, ok := byName["k8s.restart_pods"]
	if !ok {
		t.Fatal("expected k8s.restart_pods capability")
	}
	var restartSchema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(restart.InputSchema, &restartSchema); err != nil {
		t.Fatalf("unmarshal restart_pods input schema: %v", err)
	}
	if !containsRequiredString(restartSchema.Required, "mode") {
		t.Fatalf("expected restart_pods schema to require mode, got %#v", restartSchema.Required)
	}

	notify, ok := byName["notify.escalation_channel"]
	if !ok {
		t.Fatal("expected notify.escalation_channel capability")
	}
	if notify.Constraints.MaxRetries != notifyEscalationMaxRetries {
		t.Fatalf("expected notify max_retries=%d, got %d", notifyEscalationMaxRetries, notify.Constraints.MaxRetries)
	}
}

func containsRequiredString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
