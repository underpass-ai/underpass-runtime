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

	if !seen["fs.write_file"] ||
		!seen["fs.mkdir"] ||
		!seen["fs.move"] ||
		!seen["fs.copy"] ||
		!seen["fs.delete"] ||
		!seen["fs.stat"] ||
		!seen["conn.list_profiles"] ||
		!seen["conn.describe_profile"] ||
		!seen["api.benchmark"] ||
		!seen["nats.request"] ||
		!seen["nats.publish"] ||
		!seen["nats.subscribe_pull"] ||
		!seen["kafka.consume"] ||
		!seen["kafka.produce"] ||
		!seen["kafka.topic_metadata"] ||
		!seen["rabbit.consume"] ||
		!seen["rabbit.publish"] ||
		!seen["rabbit.queue_info"] ||
		!seen["redis.get"] ||
		!seen["redis.mget"] ||
		!seen["redis.scan"] ||
		!seen["redis.ttl"] ||
		!seen["redis.exists"] ||
		!seen["redis.set"] ||
		!seen["redis.del"] ||
		!seen["mongo.find"] ||
		!seen["mongo.aggregate"] ||
		!seen["git.status"] ||
		!seen["git.diff"] ||
		!seen["git.apply_patch"] ||
		!seen["git.checkout"] ||
		!seen["git.log"] ||
		!seen["git.show"] ||
		!seen["git.branch_list"] ||
		!seen["git.commit"] ||
		!seen["git.push"] ||
		!seen["git.fetch"] ||
		!seen["git.pull"] ||
		!seen["repo.detect_project_type"] ||
		!seen["repo.detect_toolchain"] ||
		!seen["repo.validate"] ||
		!seen["repo.build"] ||
		!seen["repo.test"] ||
		!seen["repo.run_tests"] ||
		!seen["repo.test_failures_summary"] ||
		!seen["repo.stacktrace_summary"] ||
		!seen["repo.changed_files"] ||
		!seen["repo.symbol_search"] ||
		!seen["repo.find_references"] ||
		!seen["repo.coverage_report"] ||
		!seen["repo.static_analysis"] ||
		!seen["repo.package"] ||
		!seen["artifact.upload"] ||
		!seen["artifact.download"] ||
		!seen["artifact.list"] ||
		!seen["image.build"] ||
		!seen["image.push"] ||
		!seen["image.inspect"] ||
		!seen["container.ps"] ||
		!seen["container.logs"] ||
		!seen["container.run"] ||
		!seen["container.exec"] ||
		!seen["k8s.get_pods"] ||
		!seen["k8s.get_services"] ||
		!seen["k8s.get_deployments"] ||
		!seen["k8s.get_images"] ||
		!seen["k8s.get_logs"] ||
		!seen["k8s.apply_manifest"] ||
		!seen["k8s.rollout_status"] ||
		!seen["k8s.restart_deployment"] ||
		!seen["security.scan_dependencies"] ||
		!seen["sbom.generate"] ||
		!seen["security.scan_secrets"] ||
		!seen["security.scan_container"] ||
		!seen["security.license_check"] ||
		!seen["quality.gate"] ||
		!seen["ci.run_pipeline"] ||
		!seen["go.mod.tidy"] ||
		!seen["go.build"] ||
		!seen["go.test"] ||
		!seen["rust.build"] ||
		!seen["node.typecheck"] ||
		!seen["python.validate"] ||
		!seen["c.build"] {
		t.Fatalf("expected critical capabilities missing: %#v", seen)
	}
}
