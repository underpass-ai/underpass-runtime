package policy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testRoleDeveloper      = "developer"
	testFieldPath          = "path"
	testFieldPaths         = "paths"
	testFieldArgs          = "args"
	testAllowedDir         = "src"
	testSandboxDevTopics   = "sandbox.,dev."
	testSandboxJobs        = "sandbox.jobs"
	testSandboxTodoCreated = "sandbox.todo.created"
)

func TestStaticPolicy_DeniesClusterScopeWithoutRole(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{Principal: domain.Principal{Roles: []string{testRoleDeveloper}}, AllowedPaths: []string{"."}},
		Capability: domain.Capability{
			Scope:     domain.ScopeCluster,
			RiskLevel: domain.RiskLow,
		},
		Approved: true,
		Args:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow {
		t.Fatal("expected decision to deny cluster scope")
	}
}

func TestStaticPolicy_RequiresApproval(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{Principal: domain.Principal{Roles: []string{testRoleDeveloper}}, AllowedPaths: []string{"."}},
		Capability: domain.Capability{
			Scope:            domain.ScopeWorkspace,
			RiskLevel:        domain.RiskMedium,
			RequiresApproval: true,
		},
		Approved: false,
		Args:     json.RawMessage(`{"path":"x.txt"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow {
		t.Fatal("expected decision to deny when approval is missing")
	}
	if decision.ErrorCode != app.ErrorCodeApprovalRequired {
		t.Fatalf("unexpected error code: %s", decision.ErrorCode)
	}
}

func TestStaticPolicy_DeniesPathOutsideAllowList(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{Principal: domain.Principal{Roles: []string{testRoleDeveloper}}, AllowedPaths: []string{testAllowedDir}},
		Capability: domain.Capability{
			Scope:     domain.ScopeWorkspace,
			RiskLevel: domain.RiskLow,
			Policy: domain.PolicyMetadata{
				PathFields: []domain.PolicyPathField{{Field: testFieldPath, WorkspaceRelative: true}},
			},
		},
		Approved: true,
		Args:     json.RawMessage(`{"path":"../outside.txt"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow {
		t.Fatal("expected decision to deny path traversal")
	}
}

func TestStaticPolicy_RuntimeRolloutRequiresToolProfile(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata:  map[string]string{},
		},
		Capability: domain.Capability{
			Name:  "k8s.get_replicasets",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow {
		t.Fatal("expected rollout tool profile denial")
	}
	if decision.ErrorCode != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", decision.ErrorCode)
	}
}

func TestStaticPolicy_RuntimeRolloutWriteRequiresEnvironmentMatch(t *testing.T) {
	t.Setenv("WORKSPACE_ENV", "prod")
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata: map[string]string{
				"tool_profile": "runtime-rollout-narrow",
				"environment":  "staging",
			},
		},
		Capability: domain.Capability{
			Name:  "k8s.rollout_pause",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow {
		t.Fatal("expected environment mismatch denial")
	}
	if decision.ErrorCode != app.ErrorCodeEnvironmentMismatch {
		t.Fatalf("unexpected error code: %s", decision.ErrorCode)
	}
}

func TestStaticPolicy_RuntimeRolloutReadAllowsMatchingProfile(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata:  map[string]string{"tool_profile": runtimeRolloutToolProfile},
		},
		Capability: domain.Capability{
			Name:  "k8s.get_replicasets",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !decision.Allow {
		t.Fatalf("expected rollout read capability to be allowed, got %#v", decision)
	}
}

func TestStaticPolicy_RuntimeRolloutWriteAllowsMatchingRuntimeEnvironment(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata: map[string]string{
				"tool_profile":        runtimeRolloutToolProfile,
				"environment":         "prod",
				"runtime_environment": "prod",
			},
		},
		Capability: domain.Capability{
			Name:  "k8s.rollout_undo",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !decision.Allow {
		t.Fatalf("expected rollout write capability to be allowed, got %#v", decision)
	}
}

func TestStaticPolicy_RuntimeRolloutHelpers(t *testing.T) {
	if !requiresRuntimeRolloutProfile("k8s.get_replicasets") {
		t.Fatal("expected k8s.get_replicasets to require rollout profile")
	}
	if requiresRuntimeRolloutProfile("shell.exec") {
		t.Fatal("did not expect unrelated capability to require rollout profile")
	}
	if !isRuntimeRolloutWriteCapability("k8s.rollout_pause") {
		t.Fatal("expected rollout pause to be treated as write capability")
	}
	if isRuntimeRolloutWriteCapability("k8s.get_replicasets") {
		t.Fatal("did not expect get_replicasets to be treated as write capability")
	}

	t.Setenv("WORKSPACE_ENV", "prod")
	if got := runtimeRolloutEnvironment(map[string]string{"runtime_environment": " staging "}); got != "staging" {
		t.Fatalf("expected metadata runtime environment, got %q", got)
	}
	if got := runtimeRolloutEnvironment(map[string]string{"runtime_environment": "unknown"}); got != "prod" {
		t.Fatalf("expected fallback WORKSPACE_ENV, got %q", got)
	}

	t.Setenv("WORKSPACE_ENV", "unknown")
	if got := runtimeRolloutEnvironment(nil); got != "" {
		t.Fatalf("expected empty runtime environment, got %q", got)
	}
}

func TestStaticPolicy_SaturationRequiresToolProfile(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata:  map[string]string{"environment": "prod", "runtime_environment": "prod"},
		},
		Capability: domain.Capability{
			Name:  "k8s.scale_deployment",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","replicas":3}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow || decision.Reason != "tool_profile_mismatch" {
		t.Fatalf("expected tool_profile_mismatch, got %#v", decision)
	}
}

func TestStaticPolicy_SaturationAllowsMatchingProfile(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"devops"}},
			Metadata: map[string]string{
				"tool_profile":        saturationToolProfile,
				"environment":         "prod",
				"runtime_environment": "prod",
			},
		},
		Capability: domain.Capability{
			Name:  "k8s.scale_deployment",
			Scope: domain.ScopeCluster,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","replicas":3}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !decision.Allow {
		t.Fatalf("expected saturation capability to be allowed, got %#v", decision)
	}
}

func TestStaticPolicy_CircuitBreakEnforcesTTLBounds(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"platform_admin"}},
			Metadata: map[string]string{
				"tool_profile":        saturationToolProfile,
				"environment":         "prod",
				"runtime_environment": "prod",
			},
		},
		Capability: domain.Capability{
			Name:      "k8s.circuit_break",
			Scope:     domain.ScopeCluster,
			RiskLevel: domain.RiskHigh,
		},
		Approved: true,
		Args:     json.RawMessage(`{"namespace":"sandbox","target_service":"api","downstream":"provider","ttl_seconds":30}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if decision.Allow || decision.Reason != "ttl_out_of_bounds" {
		t.Fatalf("expected ttl_out_of_bounds, got %#v", decision)
	}
}

func TestStaticPolicy_NotifyAllowsIncidentCommunicator(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"incident_communicator"}},
			Metadata: map[string]string{
				"tool_profile":        humanEscalationToolProfile,
				"environment":         "prod",
				"runtime_environment": "prod",
			},
		},
		Capability: domain.Capability{
			Name:  "notify.escalation_channel",
			Scope: domain.ScopeExternal,
		},
		Approved: true,
		Args:     json.RawMessage(`{"incident_id":"inc-42","handoff_node_id":"handoff:inc-42:human","summary":"Need human review","upstream_specialist":"payment-integrity-operator","upstream_decision":"escalate","reason":"provider callback missing"}`),
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if !decision.Allow {
		t.Fatalf("expected notify capability to be allowed, got %#v", decision)
	}
}

func TestAuthorizeNotifyCapability_Denials(t *testing.T) {
	t.Setenv("WORKSPACE_ENV", "prod")

	t.Run("profile_mismatch", func(t *testing.T) {
		decision := authorizeNotifyCapability(app.PolicyInput{
			Session: domain.Session{
				Principal: domain.Principal{Roles: []string{"incident_communicator"}},
				Metadata: map[string]string{
					"tool_profile":        saturationToolProfile,
					"environment":         "prod",
					"runtime_environment": "prod",
				},
			},
		})
		if decision.Allow || decision.Reason != "tool_profile_mismatch" {
			t.Fatalf("expected tool_profile_mismatch, got %#v", decision)
		}
	})

	t.Run("environment_mismatch", func(t *testing.T) {
		decision := authorizeNotifyCapability(app.PolicyInput{
			Session: domain.Session{
				Principal: domain.Principal{Roles: []string{"incident_communicator"}},
				Metadata: map[string]string{
					"tool_profile":        humanEscalationToolProfile,
					"environment":         "staging",
					"runtime_environment": "prod",
				},
			},
		})
		if decision.Allow || decision.ErrorCode != app.ErrorCodeEnvironmentMismatch {
			t.Fatalf("expected environment mismatch, got %#v", decision)
		}
	})
}

func TestAuthorizeSaturationCapability_BoundsAndPayloadErrors(t *testing.T) {
	baseInput := app.PolicyInput{
		Session: domain.Session{
			Principal: domain.Principal{Roles: []string{"platform_admin"}},
			Metadata: map[string]string{
				"tool_profile":        saturationToolProfile,
				"environment":         "prod",
				"runtime_environment": "prod",
			},
		},
	}

	t.Run("invalid_json", func(t *testing.T) {
		decision := authorizeSaturationCapability(app.PolicyInput{
			Session:    baseInput.Session,
			Capability: domain.Capability{Name: "k8s.scale_deployment"},
			Args:       json.RawMessage(`{"replicas":`),
		})
		if decision.Allow || decision.Reason != invalidArgsPayload {
			t.Fatalf("expected invalid args payload, got %#v", decision)
		}
	})

	t.Run("replicas_out_of_bounds_string", func(t *testing.T) {
		decision := authorizeSaturationCapability(app.PolicyInput{
			Session:    baseInput.Session,
			Capability: domain.Capability{Name: "k8s.scale_deployment"},
			Args:       json.RawMessage(`{"replicas":"250"}`),
		})
		if decision.Allow || decision.Reason != "replicas_out_of_bounds" {
			t.Fatalf("expected replicas_out_of_bounds, got %#v", decision)
		}
	})

	t.Run("delta_out_of_bounds", func(t *testing.T) {
		decision := authorizeSaturationCapability(app.PolicyInput{
			Session:    baseInput.Session,
			Capability: domain.Capability{Name: "k8s.scale_deployment"},
			Args:       json.RawMessage(`{"replicas_delta":-60}`),
		})
		if decision.Allow || decision.Reason != "replicas_delta_out_of_bounds" {
			t.Fatalf("expected replicas_delta_out_of_bounds, got %#v", decision)
		}
	})

	t.Run("circuit_break_ttl_string", func(t *testing.T) {
		decision := authorizeSaturationCapability(app.PolicyInput{
			Session:    baseInput.Session,
			Capability: domain.Capability{Name: "k8s.circuit_break"},
			Args:       json.RawMessage(`{"ttl_seconds":"1900"}`),
		})
		if decision.Allow || decision.Reason != "ttl_out_of_bounds" {
			t.Fatalf("expected ttl_out_of_bounds, got %#v", decision)
		}
	})
}

func TestAuthorizeEnvironmentMatchAndJSONNumberAsInt(t *testing.T) {
	t.Run("environment_mismatch_without_runtime_env", func(t *testing.T) {
		t.Setenv("WORKSPACE_ENV", "prod")
		decision, ok := authorizeEnvironmentMatch(map[string]string{"environment": "staging"})
		if !ok || decision.ErrorCode != app.ErrorCodeEnvironmentMismatch {
			t.Fatalf("expected environment mismatch, got decision=%#v ok=%v", decision, ok)
		}
	})

	t.Run("json_number_variants", func(t *testing.T) {
		cases := []struct {
			name  string
			value any
			want  int
			ok    bool
		}{
			{name: "float64", value: float64(7), want: 7, ok: true},
			{name: "int32", value: int32(8), want: 8, ok: true},
			{name: "int64", value: int64(9), want: 9, ok: true},
			{name: "json_number", value: json.Number("10"), want: 10, ok: true},
			{name: "string", value: " 11 ", want: 11, ok: true},
			{name: "invalid", value: struct{}{}, want: 0, ok: false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := jsonNumberAsInt(tc.value)
				if got != tc.want || ok != tc.ok {
					t.Fatalf("jsonNumberAsInt(%#v) = (%d,%v), want (%d,%v)", tc.value, got, ok, tc.want, tc.ok)
				}
			})
		}
	})
}

func TestStaticPolicy_PathAndArgExtractors(t *testing.T) {
	payload := map[string]any{
		testFieldPath: "src/main.go",
		testFieldPaths: []any{
			"src/a.go",
			"src/b.go",
		},
		"profile":     "dev.redis",
		"subjects":    []any{"sandbox.events.todo"},
		"topics":      []any{"sandbox.tasks"},
		"queues":      []any{testSandboxJobs},
		"keys":        []any{"sandbox:todo:1"},
		"arg":         "--run=TestTodo",
		testFieldArgs: []any{"-v", "-run=TestTodo"},
	}

	t.Run("path_single", func(t *testing.T) {
		paths, err := extractPathFieldValues(payload, domain.PolicyPathField{Field: testFieldPath})
		if err != nil || len(paths) != 1 || paths[0] != "src/main.go" {
			t.Fatalf("unexpected extractPathFieldValues single result: paths=%#v err=%v", paths, err)
		}
	})
	t.Run("path_multi", func(t *testing.T) {
		paths, err := extractPathFieldValues(payload, domain.PolicyPathField{Field: testFieldPaths, Multi: true})
		if err != nil || len(paths) != 2 {
			t.Fatalf("unexpected extractPathFieldValues multi result: paths=%#v err=%v", paths, err)
		}
	})
	t.Run("arg_single", func(t *testing.T) {
		args, err := extractArgFieldValues(payload, domain.PolicyArgField{Field: "arg"})
		if err != nil || len(args) != 1 || args[0] != "--run=TestTodo" {
			t.Fatalf("unexpected extractArgFieldValues single result: args=%#v err=%v", args, err)
		}
	})
	t.Run("arg_multi", func(t *testing.T) {
		args, err := extractArgFieldValues(payload, domain.PolicyArgField{Field: testFieldArgs, Multi: true})
		if err != nil || len(args) != 2 {
			t.Fatalf("unexpected extractArgFieldValues multi result: args=%#v err=%v", args, err)
		}
	})
	t.Run("profiles", func(t *testing.T) {
		profiles, err := extractProfileFieldValues(payload, domain.PolicyProfileField{Field: "profile"})
		if err != nil || len(profiles) != 1 {
			t.Fatalf("unexpected extractProfileFieldValues result: values=%#v err=%v", profiles, err)
		}
	})
	t.Run("subjects", func(t *testing.T) {
		subjects, err := extractSubjectFieldValues(payload, domain.PolicySubjectField{Field: "subjects", Multi: true})
		if err != nil || len(subjects) != 1 {
			t.Fatalf("unexpected extractSubjectFieldValues result: values=%#v err=%v", subjects, err)
		}
	})
	t.Run("topics", func(t *testing.T) {
		topics, err := extractTopicFieldValues(payload, domain.PolicyTopicField{Field: "topics", Multi: true})
		if err != nil || len(topics) != 1 {
			t.Fatalf("unexpected extractTopicFieldValues result: values=%#v err=%v", topics, err)
		}
	})
	t.Run("queues", func(t *testing.T) {
		queues, err := extractQueueFieldValues(payload, domain.PolicyQueueField{Field: "queues", Multi: true})
		if err != nil || len(queues) != 1 {
			t.Fatalf("unexpected extractQueueFieldValues result: values=%#v err=%v", queues, err)
		}
	})
	t.Run("keys", func(t *testing.T) {
		keys, err := extractKeyPrefixFieldValues(payload, domain.PolicyKeyPrefixField{Field: "keys", Multi: true})
		if err != nil || len(keys) != 1 {
			t.Fatalf("unexpected extractKeyPrefixFieldValues result: values=%#v err=%v", keys, err)
		}
	})
	t.Run("path_type_error", func(t *testing.T) {
		if _, err := extractPathFieldValues(payload, domain.PolicyPathField{Field: testFieldPaths}); err == nil {
			t.Fatal("expected extractPathFieldValues type error for non-multi array field")
		}
	})
	t.Run("arg_type_error", func(t *testing.T) {
		if _, err := extractArgFieldValues(payload, domain.PolicyArgField{Field: testFieldArgs}); err == nil {
			t.Fatal("expected extractArgFieldValues type error for non-multi array field")
		}
	})
}

func TestStaticPolicy_ArgumentPolicyRules(t *testing.T) {
	allowed, reason := argsAllowedByPolicy(
		json.RawMessage(`{"args":["-v","-run=TestTodo"]}`),
		[]domain.PolicyArgField{{
			Field:          testFieldArgs,
			Multi:          true,
			MaxItems:       3,
			MaxLength:      32,
			DeniedPrefix:   []string{"-exec"},
			DenyCharacters: []string{";"},
			AllowedPrefix:  []string{"-v", "-run="},
		}},
	)
	if !allowed || reason != "" {
		t.Fatalf("expected argsAllowedByPolicy allow, got allowed=%v reason=%q", allowed, reason)
	}

	allowed, reason = argsAllowedByPolicy(
		json.RawMessage(`{"args":["-v","-run=TestTodo","-race","-short"]}`),
		[]domain.PolicyArgField{{Field: testFieldArgs, Multi: true, MaxItems: 3}},
	)
	if allowed || reason == "" {
		t.Fatalf("expected max items denial, got allowed=%v reason=%q", allowed, reason)
	}

	allowed, reason = argsAllowedByPolicy(
		json.RawMessage(`{"args":["-exec=cat"]}`),
		[]domain.PolicyArgField{{Field: testFieldArgs, Multi: true, DeniedPrefix: []string{"-exec"}}},
	)
	if allowed || reason == "" {
		t.Fatalf("expected denied prefix rejection, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestStaticPolicy_MetadataGovernancePolicies(t *testing.T) {
	metadata := map[string]string{
		"allowed_profiles":           "dev.redis,dev.nats",
		"allowed_nats_subjects":      "sandbox.>,dev.>",
		"allowed_kafka_topics":       testSandboxDevTopics,
		"allowed_rabbit_queues":      testSandboxDevTopics,
		"allowed_redis_key_prefixes": "sandbox:,dev:",
	}

	profileAllowed, _ := argsAllowedByProfilePolicy(
		json.RawMessage(`{"profile_id":"dev.redis"}`),
		metadata,
		[]domain.PolicyProfileField{{Field: "profile_id"}},
	)
	if !profileAllowed {
		t.Fatal("expected profile policy allow")
	}
	profileDenied, _ := argsAllowedByProfilePolicy(
		json.RawMessage(`{"profile_id":"prod.redis"}`),
		metadata,
		[]domain.PolicyProfileField{{Field: "profile_id"}},
	)
	if profileDenied {
		t.Fatal("expected profile policy deny")
	}

	subjectAllowed, _ := argsAllowedBySubjectPolicy(
		json.RawMessage(`{"subject":"sandbox.tasks.created"}`),
		metadata,
		[]domain.PolicySubjectField{{Field: "subject"}},
	)
	if !subjectAllowed {
		t.Fatal("expected subject policy allow")
	}
	subjectDenied, _ := argsAllowedBySubjectPolicy(
		json.RawMessage(`{"subject":"prod.tasks.created"}`),
		metadata,
		[]domain.PolicySubjectField{{Field: "subject"}},
	)
	if subjectDenied {
		t.Fatal("expected subject policy deny")
	}

	topicAllowed, _ := argsAllowedByTopicPolicy(
		json.RawMessage(`{"topic":"sandbox.jobs"}`),
		metadata,
		[]domain.PolicyTopicField{{Field: "topic"}},
	)
	if !topicAllowed {
		t.Fatal("expected topic policy allow")
	}
	queueAllowed, _ := argsAllowedByQueuePolicy(
		json.RawMessage(`{"queue":"dev.jobs"}`),
		metadata,
		[]domain.PolicyQueueField{{Field: "queue"}},
	)
	if !queueAllowed {
		t.Fatal("expected queue policy allow")
	}
	keyAllowed, _ := argsAllowedByKeyPrefixPolicy(
		json.RawMessage(`{"key":"sandbox:todo:1"}`),
		metadata,
		[]domain.PolicyKeyPrefixField{{Field: "key"}},
	)
	if !keyAllowed {
		t.Fatal("expected key prefix policy allow")
	}
}

func TestStaticPolicy_MatchersAndUtilities(t *testing.T) {
	t.Run("wildcard_patterns", func(t *testing.T) {
		if !wildcardPatternMatch("*", testSandboxJobs) {
			t.Fatal("expected wildcard * to match any value")
		}
		if !wildcardPatternMatch("sandbox.", testSandboxJobs) {
			t.Fatal("expected prefix-style wildcard pattern to match")
		}
		if !wildcardPatternMatch("sandbox.*.jobs", "sandbox.todo.jobs") {
			t.Fatal("expected middle wildcard pattern to match")
		}
		if wildcardPatternMatch("sandbox.*.jobs", "sandbox.todo.worker") {
			t.Fatal("did not expect wildcard pattern match")
		}
	})

	t.Run("nats_subject_match", func(t *testing.T) {
		if !natsSubjectMatch("sandbox.*.created", testSandboxTodoCreated) {
			t.Fatal("expected nats * match")
		}
		if !natsSubjectMatch("sandbox.>", testSandboxTodoCreated) {
			t.Fatal("expected nats > match")
		}
		if natsSubjectMatch("sandbox.todo", testSandboxTodoCreated) {
			t.Fatal("did not expect exact nats subject match")
		}
	})

	t.Run("prefix_allowlist_match", func(t *testing.T) {
		allowlist := map[string]bool{"sandbox:": true}
		if !prefixAllowlistMatch("sandbox:todo:1", allowlist) {
			t.Fatal("expected prefix allowlist match")
		}
		if prefixAllowlistMatch("prod:todo:1", allowlist) {
			t.Fatal("did not expect prefix allowlist match")
		}
	})

	t.Run("parsers", func(t *testing.T) {
		parsedProfiles := parseAllowedProfiles(map[string]string{"allowed_profiles": "dev.redis, dev.nats"})
		if !parsedProfiles["dev.redis"] || !parsedProfiles["dev.nats"] {
			t.Fatalf("unexpected parseAllowedProfiles result: %#v", parsedProfiles)
		}
		parsedSubjects := parseAllowedNATSSubjects(map[string]string{"allowed_nats_subjects": "sandbox.>,dev.>"})
		if !parsedSubjects["sandbox.>"] || !parsedSubjects["dev.>"] {
			t.Fatalf("unexpected parseAllowedNATSSubjects result: %#v", parsedSubjects)
		}
		parsedAllowlist := parseAllowlist(map[string]string{"allowed_kafka_topics": testSandboxDevTopics}, "allowed_kafka_topics")
		if !parsedAllowlist["sandbox."] || !parsedAllowlist["dev."] {
			t.Fatalf("unexpected parseAllowlist result: %#v", parsedAllowlist)
		}
	})

	t.Run("lookupField", func(t *testing.T) {
		payload := map[string]any{"nested": map[string]any{"value": "ok"}}
		value, found := lookupField(payload, []string{"nested", "value"})
		if !found || value.(string) != "ok" {
			t.Fatalf("unexpected lookupField result: value=%#v found=%v", value, found)
		}
		if _, found := lookupField(payload, []string{"nested", "missing"}); found {
			t.Fatal("expected lookupField miss")
		}
	})

	t.Run("isPathWithinAllowlist", func(t *testing.T) {
		if !isPathWithinAllowlist("src/app/main.go", []string{testAllowedDir}) {
			t.Fatal("expected path within allowlist")
		}
		if isPathWithinAllowlist("../outside", []string{testAllowedDir}) {
			t.Fatal("expected path outside allowlist")
		}
	})
}
