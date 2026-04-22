package tools

import (
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testMissingCapabilityFmt        = "missing capability %q"
	testExpectedLangOutputSchemaFmt = "%s: expected lang tool OutputSchema"
)

// langToolOutputSchema is the expected JSON schema for language-specific toolchain tools.
var langToolOutputSchema = json.RawMessage(`{"type":"object","properties":{"exit_code":{"type":"integer"},"compiled_binary_path":{"type":"string"},"coverage_percent":{"type":"number"},"diagnostics":{"type":"array","items":{"type":"string"}}}}`)

// TestDefaultCapabilities_PolicyConsistency verifies that capabilities loaded from
// YAML have correct shared policies (git remote, extra_args, k8s read, etc.).
func TestDefaultCapabilities_PolicyConsistency(t *testing.T) {
	capabilities := DefaultCapabilities()
	capMap := make(map[string]domain.Capability)
	for _, c := range capabilities {
		capMap[c.Name] = c
	}

	t.Run("git_remote_tools", func(t *testing.T) {
		for _, name := range []string{"git.push", "git.fetch", "git.pull"} {
			requireGitRemotePolicy(t, capMap, name)
		}
	})

	t.Run("repo_extra_args_tools", func(t *testing.T) {
		for _, name := range []string{"repo.build", "repo.test", "repo.run_tests", "repo.test_failures_summary", "repo.stacktrace_summary"} {
			requireExtraArgsPolicy(t, capMap, name)
		}
	})

	t.Run("k8s_read_tools", func(t *testing.T) {
		for _, name := range []string{"k8s.get_pods", "k8s.get_services", "k8s.get_deployments"} {
			requireK8sReadPolicy(t, capMap, name)
		}
		requireK8sReplicaSetReadPolicy(t, capMap, "k8s.get_replicasets")
	})

	t.Run("go_tools", func(t *testing.T) {
		for _, name := range []string{"go.build", "go.test"} {
			cap := requireCapability(t, capMap, name)
			if cap.Scope != domain.ScopeRepo {
				t.Fatalf("%s: expected ScopeRepo, got %v", name, cap.Scope)
			}
			if cap.CostHint != "high" {
				t.Fatalf("%s: expected CostHint=high, got %q", name, cap.CostHint)
			}
			requireLangOutputSchema(t, cap, name)
		}
	})

	t.Run("rust_tools", func(t *testing.T) {
		for _, name := range []string{"rust.build", "rust.test", "rust.clippy"} {
			cap := requireCapability(t, capMap, name)
			if cap.Scope != domain.ScopeRepo {
				t.Fatalf("%s: expected ScopeRepo, got %v", name, cap.Scope)
			}
			requireLangOutputSchema(t, cap, name)
		}
	})

	t.Run("node_tools", func(t *testing.T) {
		for _, name := range []string{"node.build", "node.test", "node.lint", "node.typecheck"} {
			cap := requireCapability(t, capMap, name)
			if len(cap.Policy.PathFields) != 1 || cap.Policy.PathFields[0].Field != "target" {
				t.Fatalf("%s: expected PathFields with target, got %#v", name, cap.Policy.PathFields)
			}
			requireLangOutputSchema(t, cap, name)
		}
	})
}

func requireCapability(t *testing.T, capMap map[string]domain.Capability, name string) domain.Capability {
	t.Helper()
	cap, ok := capMap[name]
	if !ok {
		t.Fatalf(testMissingCapabilityFmt, name)
	}
	return cap
}

func requireGitRemotePolicy(t *testing.T, capMap map[string]domain.Capability, name string) {
	t.Helper()
	cap := requireCapability(t, capMap, name)
	if len(cap.Policy.ArgFields) != 2 {
		t.Fatalf("%s: expected 2 policy ArgFields, got %d", name, len(cap.Policy.ArgFields))
	}
	if cap.Policy.ArgFields[0].Field != "remote" {
		t.Fatalf("%s: expected first ArgField=remote, got %q", name, cap.Policy.ArgFields[0].Field)
	}
	if cap.Policy.ArgFields[1].Field != "refspec" {
		t.Fatalf("%s: expected second ArgField=refspec, got %q", name, cap.Policy.ArgFields[1].Field)
	}
}

func requireExtraArgsPolicy(t *testing.T, capMap map[string]domain.Capability, name string) {
	t.Helper()
	cap := requireCapability(t, capMap, name)
	if len(cap.Policy.ArgFields) != 1 {
		t.Fatalf("%s: expected 1 policy ArgField, got %d", name, len(cap.Policy.ArgFields))
	}
	af := cap.Policy.ArgFields[0]
	if af.Field != "extra_args" {
		t.Fatalf("%s: expected ArgField=extra_args, got %q", name, af.Field)
	}
	if !af.Multi {
		t.Fatalf("%s: expected Multi=true", name)
	}
	if af.MaxItems != 8 {
		t.Fatalf("%s: expected MaxItems=8, got %d", name, af.MaxItems)
	}
}

func requireK8sReadPolicy(t *testing.T, capMap map[string]domain.Capability, name string) {
	t.Helper()
	cap := requireCapability(t, capMap, name)
	if cap.Scope != domain.ScopeCluster {
		t.Fatalf("%s: expected ScopeCluster, got %v", name, cap.Scope)
	}
	if cap.RiskLevel != domain.RiskLow {
		t.Fatalf("%s: expected RiskLow, got %v", name, cap.RiskLevel)
	}
	if cap.Constraints.TimeoutSeconds != 30 {
		t.Fatalf("%s: expected TimeoutSeconds=30, got %d", name, cap.Constraints.TimeoutSeconds)
	}
	if len(cap.Policy.NamespaceFields) != 1 {
		t.Fatalf("%s: expected 1 NamespaceField, got %d", name, len(cap.Policy.NamespaceFields))
	}
}

func requireK8sReplicaSetReadPolicy(t *testing.T, capMap map[string]domain.Capability, name string) {
	t.Helper()
	cap := requireCapability(t, capMap, name)
	if cap.Scope != domain.ScopeCluster {
		t.Fatalf("%s: expected ScopeCluster, got %v", name, cap.Scope)
	}
	if cap.RiskLevel != domain.RiskLow {
		t.Fatalf("%s: expected RiskLow, got %v", name, cap.RiskLevel)
	}
	if cap.Constraints.TimeoutSeconds != 10 {
		t.Fatalf("%s: expected TimeoutSeconds=10, got %d", name, cap.Constraints.TimeoutSeconds)
	}
	if len(cap.Policy.NamespaceFields) != 1 {
		t.Fatalf("%s: expected 1 NamespaceField, got %d", name, len(cap.Policy.NamespaceFields))
	}
	if len(cap.Policy.ArgFields) != 1 || cap.Policy.ArgFields[0].Field != "deployment_name" {
		t.Fatalf("%s: expected deployment_name arg policy, got %#v", name, cap.Policy.ArgFields)
	}
}

func requireLangOutputSchema(t *testing.T, cap domain.Capability, name string) {
	t.Helper()
	if string(cap.OutputSchema) != string(langToolOutputSchema) {
		t.Fatalf(testExpectedLangOutputSchemaFmt, name)
	}
}

// TestDefaultCapabilities_ShellDenyChars verifies that shared deny char lists
// are correctly expanded from YAML anchors into relevant policies.
func TestDefaultCapabilities_ShellDenyChars(t *testing.T) {
	expectedDeny := []string{";", "|", "&", "`", "$(", ">", "<", "\n", "\r"}
	capabilities := DefaultCapabilities()
	capMap := make(map[string]domain.Capability)
	for _, c := range capabilities {
		capMap[c.Name] = c
	}

	// git.push uses git_remote policy — remote field should have shell_deny_space.
	push := capMap["git.push"]
	remote := push.Policy.ArgFields[0]
	expectedWithSpace := append(expectedDeny, " ")
	if len(remote.DenyCharacters) != len(expectedWithSpace) {
		t.Fatalf("git.push remote: expected %d DenyCharacters, got %d",
			len(expectedWithSpace), len(remote.DenyCharacters))
	}

	// repo.test uses extra_args_test policy — extra_args should have shell_deny.
	repoTest := capMap["repo.test"]
	af := repoTest.Policy.ArgFields[0]
	if len(af.DenyCharacters) != len(expectedDeny) {
		t.Fatalf("repo.test extra_args: expected %d DenyCharacters, got %d",
			len(expectedDeny), len(af.DenyCharacters))
	}
}

// TestDefaultCapabilities_LangToolOutputSchema validates the lang tool output
// schema has the expected properties.
func TestDefaultCapabilities_LangToolOutputSchema(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal(langToolOutputSchema, &parsed); err != nil {
		t.Fatalf("langToolOutputSchema is not valid JSON: %v", err)
	}
	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in output schema")
	}
	for _, key := range []string{"exit_code", "compiled_binary_path", "coverage_percent", "diagnostics"} {
		if _, found := props[key]; !found {
			t.Fatalf("expected property %q in langToolOutputSchema", key)
		}
	}
}
