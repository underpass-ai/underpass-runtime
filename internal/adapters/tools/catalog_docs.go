package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type capabilityFamily struct {
	Name  string
	Tools []domain.Capability
}

// DefaultCapabilitiesMarkdown renders a deterministic markdown snapshot from DefaultCapabilities.
func DefaultCapabilitiesMarkdown() string {
	return CapabilitiesMarkdown(DefaultCapabilities())
}

// CapabilitiesMarkdown renders a deterministic markdown snapshot from capabilities.
func CapabilitiesMarkdown(capabilities []domain.Capability) string {
	cloned := make([]domain.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		cloned = append(cloned, capability)
	}
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].Name < cloned[j].Name
	})

	families := map[string][]domain.Capability{}
	order := make([]string, 0)
	for _, capability := range cloned {
		family := capabilityFamilyName(capability.Name)
		if _, found := families[family]; !found {
			order = append(order, family)
		}
		families[family] = append(families[family], capability)
	}
	sort.Strings(order)

	var builder strings.Builder
	builder.WriteString("# Workspace Capability Catalog\n\n")
	builder.WriteString("This file is generated from `internal/adapters/tools/DefaultCapabilities()`.\n")
	builder.WriteString("Do not edit manually. Regenerate with `make catalog-docs`.\n\n")
	builder.WriteString(fmt.Sprintf("- Total capabilities: `%d`\n", len(cloned)))
	builder.WriteString(fmt.Sprintf("- Families: `%d`\n\n", len(order)))

	for _, familyName := range order {
		builder.WriteString(fmt.Sprintf("## %s\n\n", familyName))
		builder.WriteString("| Tool | Scope | Risk | Approval | Side Effects | Idempotency |\n")
		builder.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		for _, capability := range families[familyName] {
			approval := "no"
			if capability.RequiresApproval {
				approval = "yes"
			}
			builder.WriteString(fmt.Sprintf(
				"| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
				capability.Name,
				capability.Scope,
				capability.RiskLevel,
				approval,
				capability.SideEffects,
				capability.Idempotency,
			))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

func capabilityFamilyName(toolName string) string {
	trimmed := strings.TrimSpace(toolName)
	if trimmed == "" {
		return "misc"
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) <= 1 {
		return trimmed
	}
	return parts[0] + ".*"
}
