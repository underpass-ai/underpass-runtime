package tools

import "testing"

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
		"kafka.consume", "kafka.produce", "kafka.topic_metadata",
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
		"k8s.get_pods", "k8s.get_services", "k8s.get_deployments", "k8s.get_images",
		"k8s.get_logs", "k8s.apply_manifest", "k8s.rollout_status", "k8s.restart_deployment",
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
