package policy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestStaticPolicy_DeniesClusterScopeWithoutRole(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{Principal: domain.Principal{Roles: []string{"developer"}}, AllowedPaths: []string{"."}},
		Capability: domain.Capability{
			Scope:     domain.ScopeCluster,
			RiskLevel: domain.RiskLow,
		},
		Approved: true,
		Args:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allow {
		t.Fatal("expected decision to deny cluster scope")
	}
}

func TestStaticPolicy_RequiresApproval(t *testing.T) {
	engine := NewStaticPolicy()
	decision, err := engine.Authorize(context.Background(), app.PolicyInput{
		Session: domain.Session{Principal: domain.Principal{Roles: []string{"developer"}}, AllowedPaths: []string{"."}},
		Capability: domain.Capability{
			Scope:            domain.ScopeWorkspace,
			RiskLevel:        domain.RiskMedium,
			RequiresApproval: true,
		},
		Approved: false,
		Args:     json.RawMessage(`{"path":"x.txt"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
		Session: domain.Session{Principal: domain.Principal{Roles: []string{"developer"}}, AllowedPaths: []string{"src"}},
		Capability: domain.Capability{
			Scope:     domain.ScopeWorkspace,
			RiskLevel: domain.RiskLow,
			Policy: domain.PolicyMetadata{
				PathFields: []domain.PolicyPathField{{Field: "path", WorkspaceRelative: true}},
			},
		},
		Approved: true,
		Args:     json.RawMessage(`{"path":"../outside.txt"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allow {
		t.Fatal("expected decision to deny path traversal")
	}
}

func TestStaticPolicy_PathAndArgExtractors(t *testing.T) {
	payload := map[string]any{
		"path": "src/main.go",
		"paths": []any{
			"src/a.go",
			"src/b.go",
		},
		"profile": "dev.redis",
		"subjects": []any{"sandbox.events.todo"},
		"topics":   []any{"sandbox.tasks"},
		"queues":   []any{"sandbox.jobs"},
		"keys":     []any{"sandbox:todo:1"},
		"arg":      "--run=TestTodo",
		"args":     []any{"-v", "-run=TestTodo"},
	}

	paths, err := extractPathFieldValues(payload, domain.PolicyPathField{Field: "path"})
	if err != nil || len(paths) != 1 || paths[0] != "src/main.go" {
		t.Fatalf("unexpected extractPathFieldValues single result: paths=%#v err=%v", paths, err)
	}
	paths, err = extractPathFieldValues(payload, domain.PolicyPathField{Field: "paths", Multi: true})
	if err != nil || len(paths) != 2 {
		t.Fatalf("unexpected extractPathFieldValues multi result: paths=%#v err=%v", paths, err)
	}

	args, err := extractArgFieldValues(payload, domain.PolicyArgField{Field: "arg"})
	if err != nil || len(args) != 1 || args[0] != "--run=TestTodo" {
		t.Fatalf("unexpected extractArgFieldValues single result: args=%#v err=%v", args, err)
	}
	args, err = extractArgFieldValues(payload, domain.PolicyArgField{Field: "args", Multi: true})
	if err != nil || len(args) != 2 {
		t.Fatalf("unexpected extractArgFieldValues multi result: args=%#v err=%v", args, err)
	}

	profiles, err := extractProfileFieldValues(payload, domain.PolicyProfileField{Field: "profile"})
	if err != nil || len(profiles) != 1 {
		t.Fatalf("unexpected extractProfileFieldValues result: values=%#v err=%v", profiles, err)
	}
	subjects, err := extractSubjectFieldValues(payload, domain.PolicySubjectField{Field: "subjects", Multi: true})
	if err != nil || len(subjects) != 1 {
		t.Fatalf("unexpected extractSubjectFieldValues result: values=%#v err=%v", subjects, err)
	}
	topics, err := extractTopicFieldValues(payload, domain.PolicyTopicField{Field: "topics", Multi: true})
	if err != nil || len(topics) != 1 {
		t.Fatalf("unexpected extractTopicFieldValues result: values=%#v err=%v", topics, err)
	}
	queues, err := extractQueueFieldValues(payload, domain.PolicyQueueField{Field: "queues", Multi: true})
	if err != nil || len(queues) != 1 {
		t.Fatalf("unexpected extractQueueFieldValues result: values=%#v err=%v", queues, err)
	}
	keys, err := extractKeyPrefixFieldValues(payload, domain.PolicyKeyPrefixField{Field: "keys", Multi: true})
	if err != nil || len(keys) != 1 {
		t.Fatalf("unexpected extractKeyPrefixFieldValues result: values=%#v err=%v", keys, err)
	}

	if _, err := extractPathFieldValues(payload, domain.PolicyPathField{Field: "paths"}); err == nil {
		t.Fatal("expected extractPathFieldValues type error for non-multi array field")
	}
	if _, err := extractArgFieldValues(payload, domain.PolicyArgField{Field: "args"}); err == nil {
		t.Fatal("expected extractArgFieldValues type error for non-multi array field")
	}
}

func TestStaticPolicy_ArgumentPolicyRules(t *testing.T) {
	allowed, reason := argsAllowedByPolicy(
		json.RawMessage(`{"args":["-v","-run=TestTodo"]}`),
		[]domain.PolicyArgField{{
			Field:         "args",
			Multi:         true,
			MaxItems:      3,
			MaxLength:     32,
			DeniedPrefix:  []string{"-exec"},
			DenyCharacters: []string{";"},
			AllowedPrefix: []string{"-v", "-run="},
		}},
	)
	if !allowed || reason != "" {
		t.Fatalf("expected argsAllowedByPolicy allow, got allowed=%v reason=%q", allowed, reason)
	}

	allowed, reason = argsAllowedByPolicy(
		json.RawMessage(`{"args":["-v","-run=TestTodo","-race","-short"]}`),
		[]domain.PolicyArgField{{Field: "args", Multi: true, MaxItems: 3}},
	)
	if allowed || reason == "" {
		t.Fatalf("expected max items denial, got allowed=%v reason=%q", allowed, reason)
	}

	allowed, reason = argsAllowedByPolicy(
		json.RawMessage(`{"args":["-exec=cat"]}`),
		[]domain.PolicyArgField{{Field: "args", Multi: true, DeniedPrefix: []string{"-exec"}}},
	)
	if allowed || reason == "" {
		t.Fatalf("expected denied prefix rejection, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestStaticPolicy_MetadataGovernancePolicies(t *testing.T) {
	metadata := map[string]string{
		"allowed_profiles":           "dev.redis,dev.nats",
		"allowed_nats_subjects":      "sandbox.>,dev.>",
		"allowed_kafka_topics":       "sandbox.,dev.",
		"allowed_rabbit_queues":      "sandbox.,dev.",
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
	if !wildcardPatternMatch("*", "sandbox.jobs") {
		t.Fatal("expected wildcard * to match any value")
	}
	if !wildcardPatternMatch("sandbox.", "sandbox.jobs") {
		t.Fatal("expected prefix-style wildcard pattern to match")
	}
	if !wildcardPatternMatch("sandbox.*.jobs", "sandbox.todo.jobs") {
		t.Fatal("expected middle wildcard pattern to match")
	}
	if wildcardPatternMatch("sandbox.*.jobs", "sandbox.todo.worker") {
		t.Fatal("did not expect wildcard pattern match")
	}

	if !natsSubjectMatch("sandbox.*.created", "sandbox.todo.created") {
		t.Fatal("expected nats * match")
	}
	if !natsSubjectMatch("sandbox.>", "sandbox.todo.created") {
		t.Fatal("expected nats > match")
	}
	if natsSubjectMatch("sandbox.todo", "sandbox.todo.created") {
		t.Fatal("did not expect exact nats subject match")
	}

	allowlist := map[string]bool{"sandbox:": true}
	if !prefixAllowlistMatch("sandbox:todo:1", allowlist) {
		t.Fatal("expected prefix allowlist match")
	}
	if prefixAllowlistMatch("prod:todo:1", allowlist) {
		t.Fatal("did not expect prefix allowlist match")
	}

	parsedProfiles := parseAllowedProfiles(map[string]string{"allowed_profiles": "dev.redis, dev.nats"})
	if !parsedProfiles["dev.redis"] || !parsedProfiles["dev.nats"] {
		t.Fatalf("unexpected parseAllowedProfiles result: %#v", parsedProfiles)
	}
	parsedSubjects := parseAllowedNATSSubjects(map[string]string{"allowed_nats_subjects": "sandbox.>,dev.>"})
	if !parsedSubjects["sandbox.>"] || !parsedSubjects["dev.>"] {
		t.Fatalf("unexpected parseAllowedNATSSubjects result: %#v", parsedSubjects)
	}
	parsedAllowlist := parseAllowlist(map[string]string{"allowed_kafka_topics": "sandbox.,dev."}, "allowed_kafka_topics")
	if !parsedAllowlist["sandbox."] || !parsedAllowlist["dev."] {
		t.Fatalf("unexpected parseAllowlist result: %#v", parsedAllowlist)
	}

	payload := map[string]any{"nested": map[string]any{"value": "ok"}}
	value, found := lookupField(payload, []string{"nested", "value"})
	if !found || value.(string) != "ok" {
		t.Fatalf("unexpected lookupField result: value=%#v found=%v", value, found)
	}
	if _, found := lookupField(payload, []string{"nested", "missing"}); found {
		t.Fatal("expected lookupField miss")
	}

	if !isPathWithinAllowlist("src/app/main.go", []string{"src"}) {
		t.Fatal("expected path within allowlist")
	}
	if isPathWithinAllowlist("../outside", []string{"src"}) {
		t.Fatal("expected path outside allowlist")
	}
}
