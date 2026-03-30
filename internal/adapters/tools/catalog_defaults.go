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
	Name             string                `yaml:"name"`
	Description      string                `yaml:"description"`
	InputSchema      string                `yaml:"input_schema"`
	OutputSchema     string                `yaml:"output_schema"`
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
	caps, err := loadCatalog(data)
	if err != nil {
		panic(err.Error())
	}
	return caps
}

// loadCatalog parses the embedded YAML into domain capabilities.
// Returns a descriptive error instead of panicking so callers can handle
// startup failures gracefully when needed.
func loadCatalog(data []byte) ([]domain.Capability, error) {
	var cat yamlCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("catalog_defaults.yaml: %w", err)
	}

	caps := make([]domain.Capability, 0, len(cat.Capabilities))
	for i, yc := range cat.Capabilities {
		if yc.Name == "" {
			return nil, fmt.Errorf("catalog_defaults.yaml[%d]: missing name", i)
		}

		inputSchema, err := validJSON(yc.InputSchema, yc.Name, "input_schema")
		if err != nil {
			return nil, err
		}
		outputSchema, err := validJSON(yc.OutputSchema, yc.Name, "output_schema")
		if err != nil {
			return nil, err
		}

		var examples []json.RawMessage
		for j, ex := range yc.Examples {
			raw, err := validJSON(ex, yc.Name, fmt.Sprintf("examples[%d]", j))
			if err != nil {
				return nil, err
			}
			examples = append(examples, raw)
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
	return caps, nil
}

func validJSON(s, capName, field string) (json.RawMessage, error) {
	if s == "" {
		return nil, fmt.Errorf("catalog %q: empty %s", capName, field)
	}
	raw := json.RawMessage(s)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("catalog %q: invalid JSON in %s: %s", capName, field, s)
	}
	return raw, nil
}
