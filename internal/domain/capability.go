package domain

import "encoding/json"

type Scope string

const (
	ScopeRepo      Scope = "repo"
	ScopeWorkspace Scope = "workspace"
	ScopeCluster   Scope = "cluster"
	ScopeExternal  Scope = "external"
)

type SideEffects string

const (
	SideEffectsNone         SideEffects = "none"
	SideEffectsReversible   SideEffects = "reversible"
	SideEffectsIrreversible SideEffects = "irreversible"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type Idempotency string

const (
	IdempotencyGuaranteed Idempotency = "guaranteed"
	IdempotencyBestEffort Idempotency = "best-effort"
	IdempotencyNone       Idempotency = "none"
)

type Observability struct {
	TraceName string `json:"trace_name" yaml:"trace_name"`
	SpanName  string `json:"span_name" yaml:"span_name"`
}

type PolicyPathField struct {
	Field             string `json:"field" yaml:"field"`
	Multi             bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
	WorkspaceRelative bool   `json:"workspace_relative,omitempty" yaml:"workspace_relative,omitempty"`
}

type PolicyArgField struct {
	Field          string   `json:"field" yaml:"field"`
	Multi          bool     `json:"multi,omitempty" yaml:"multi,omitempty"`
	MaxItems       int      `json:"max_items,omitempty" yaml:"max_items,omitempty"`
	MaxLength      int      `json:"max_length,omitempty" yaml:"max_length,omitempty"`
	AllowedValues  []string `json:"allowed_values,omitempty" yaml:"allowed_values,omitempty"`
	AllowedPrefix  []string `json:"allowed_prefix,omitempty" yaml:"allowed_prefix,omitempty"`
	DeniedPrefix   []string `json:"denied_prefix,omitempty" yaml:"denied_prefix,omitempty"`
	DenyCharacters []string `json:"deny_characters,omitempty" yaml:"deny_characters,omitempty"`
}

type PolicyProfileField struct {
	Field string `json:"field" yaml:"field"`
	Multi bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
}

type PolicySubjectField struct {
	Field string `json:"field" yaml:"field"`
	Multi bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
}

type PolicyTopicField struct {
	Field string `json:"field" yaml:"field"`
	Multi bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
}

type PolicyQueueField struct {
	Field string `json:"field" yaml:"field"`
	Multi bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
}

type PolicyKeyPrefixField struct {
	Field string `json:"field" yaml:"field"`
	Multi bool   `json:"multi,omitempty" yaml:"multi,omitempty"`
}

type PolicyMetadata struct {
	PathFields      []PolicyPathField      `json:"path_fields,omitempty" yaml:"path_fields,omitempty"`
	ArgFields       []PolicyArgField       `json:"arg_fields,omitempty" yaml:"arg_fields,omitempty"`
	ProfileFields   []PolicyProfileField   `json:"profile_fields,omitempty" yaml:"profile_fields,omitempty"`
	SubjectFields   []PolicySubjectField   `json:"subject_fields,omitempty" yaml:"subject_fields,omitempty"`
	TopicFields     []PolicyTopicField     `json:"topic_fields,omitempty" yaml:"topic_fields,omitempty"`
	QueueFields     []PolicyQueueField     `json:"queue_fields,omitempty" yaml:"queue_fields,omitempty"`
	KeyPrefixFields []PolicyKeyPrefixField `json:"key_prefix_fields,omitempty" yaml:"key_prefix_fields,omitempty"`
	NamespaceFields []string               `json:"namespace_fields,omitempty" yaml:"namespace_fields,omitempty"`
	RegistryFields  []string               `json:"registry_fields,omitempty" yaml:"registry_fields,omitempty"`
}

type Constraints struct {
	TimeoutSeconds int      `json:"timeout_seconds" yaml:"timeout_seconds"`
	MaxRetries     int      `json:"max_retries" yaml:"max_retries,omitempty"`
	AllowedPaths   []string `json:"allowed_paths,omitempty" yaml:"allowed_paths,omitempty"`
	OutputLimitKB  int      `json:"output_limit_kb,omitempty" yaml:"output_limit_kb,omitempty"`
}

type Capability struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	InputSchema      json.RawMessage   `json:"input_schema"`
	OutputSchema     json.RawMessage   `json:"output_schema"`
	Scope            Scope             `json:"scope"`
	SideEffects      SideEffects       `json:"side_effects"`
	RiskLevel        RiskLevel         `json:"risk_level"`
	RequiresApproval bool              `json:"requires_approval"`
	Idempotency      Idempotency       `json:"idempotency"`
	Constraints      Constraints       `json:"constraints"`
	Preconditions    []string          `json:"preconditions,omitempty"`
	Postconditions   []string          `json:"postconditions,omitempty"`
	CostHint         string            `json:"cost_hint,omitempty"`
	Policy           PolicyMetadata    `json:"policy,omitempty"`
	Observability    Observability     `json:"observability"`
	Examples         []json.RawMessage `json:"examples,omitempty"`
}
