package tools

import (
	_ "embed" // required for //go:embed directive
	"encoding/json"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

//go:embed catalog_defaults.yaml
var catalogYAML []byte

// defaultTraceName is the shared OpenTelemetry trace name for all catalog tools.
const defaultTraceName = "workspace.tools"

// yamlCatalog is the top-level YAML structure.
type yamlCatalog struct {
	Capabilities []yamlCapability `yaml:"capabilities"`
}

// yamlCapability mirrors domain.Capability with string fields for JSON schemas.
type yamlCapability struct {
	Name             string               `yaml:"name"`
	Description      string               `yaml:"description"`
	InputSchema      string               `yaml:"input_schema"`
	OutputSchema     string               `yaml:"output_schema"`
	Scope            domain.Scope          `yaml:"scope"`
	SideEffects      domain.SideEffects    `yaml:"side_effects"`
	RiskLevel        domain.RiskLevel      `yaml:"risk_level"`
	RequiresApproval bool                  `yaml:"requires_approval"`
	Idempotency      domain.Idempotency    `yaml:"idempotency"`
	Constraints      domain.Constraints    `yaml:"constraints"`
	Preconditions    []string              `yaml:"preconditions"`
	Postconditions   []string              `yaml:"postconditions"`
	CostHint         string                `yaml:"cost_hint"`
	Policy           domain.PolicyMetadata `yaml:"policy"`
	Examples         []string              `yaml:"examples"`
}

var (
	defaultCapsOnce sync.Once
	defaultCaps     []domain.Capability
)

// DefaultCapabilities returns the full set of workspace tool capabilities,
// loaded from the embedded YAML catalog.
func DefaultCapabilities() []domain.Capability {
	defaultCapsOnce.Do(func() {
		defaultCaps = mustLoadCatalog(catalogYAML)
	})
	// Return a fresh copy each call so callers can't mutate the cached slice.
	out := make([]domain.Capability, len(defaultCaps))
	copy(out, defaultCaps)
	return out
}

func mustLoadCatalog(data []byte) []domain.Capability {
	var cat yamlCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		panic(fmt.Sprintf("catalog_defaults.yaml: %v", err))
	}

	caps := make([]domain.Capability, 0, len(cat.Capabilities))
	for i, yc := range cat.Capabilities {
		if yc.Name == "" {
			panic(fmt.Sprintf("catalog_defaults.yaml[%d]: missing name", i))
		}

		inputSchema := mustValidJSON(yc.InputSchema, yc.Name, "input_schema")
		outputSchema := mustValidJSON(yc.OutputSchema, yc.Name, "output_schema")

		var examples []json.RawMessage
		for j, ex := range yc.Examples {
			examples = append(examples, mustValidJSON(ex, yc.Name, fmt.Sprintf("examples[%d]", j)))
		}

		// span_name defaults to capability name; trace_name is always the constant.
		spanName := yc.Name

		caps = append(caps, domain.Capability{
			Name:             yc.Name,
			Description:      yc.Description,
			InputSchema:      inputSchema,
			OutputSchema:     outputSchema,
			Scope:            yc.Scope,
			SideEffects:      yc.SideEffects,
			RiskLevel:        yc.RiskLevel,
			RequiresApproval: yc.RequiresApproval,
			Idempotency:      yc.Idempotency,
			Constraints:      yc.Constraints,
			Preconditions:    yc.Preconditions,
			Postconditions:   yc.Postconditions,
			CostHint:         yc.CostHint,
			Policy:           yc.Policy,
			Observability:    domain.Observability{TraceName: defaultTraceName, SpanName: spanName},
			Examples:         examples,
		})
	}
	return caps
}

func mustValidJSON(s, capName, field string) json.RawMessage {
	if s == "" {
		panic(fmt.Sprintf("catalog %q: empty %s", capName, field))
	}
	raw := json.RawMessage(s)
	if !json.Valid(raw) {
		panic(fmt.Sprintf("catalog %q: invalid JSON in %s: %s", capName, field, s))
	}
	return raw
}
