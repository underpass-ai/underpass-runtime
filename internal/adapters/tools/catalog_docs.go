package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// DefaultCapabilitiesMarkdown renders a deterministic markdown snapshot from DefaultCapabilities.
func DefaultCapabilitiesMarkdown() string {
	return CapabilitiesMarkdown(DefaultCapabilities())
}

// CapabilitiesMarkdown renders a deterministic markdown snapshot from capabilities.
func CapabilitiesMarkdown(capabilities []domain.Capability) string {
	cloned := make([]domain.Capability, 0, len(capabilities))
	cloned = append(cloned, capabilities...)
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].Name < cloned[j].Name
	})

	families := map[string][]domain.Capability{}
	order := make([]string, 0)
	for i := range cloned {
		capability := &cloned[i]
		family := capabilityFamilyName(capability.Name)
		if _, found := families[family]; !found {
			order = append(order, family)
		}
		families[family] = append(families[family], *capability)
	}
	sort.Strings(order)

	var builder strings.Builder
	builder.WriteString("# Workspace Capability Catalog\n\n")
	builder.WriteString("This file is generated from `internal/adapters/tools/DefaultCapabilities()`.\n")
	builder.WriteString("Do not edit manually. Regenerate with `make catalog-docs`.\n\n")
	fmt.Fprintf(&builder, "- Total capabilities: `%d`\n", len(cloned))
	fmt.Fprintf(&builder, "- Families: `%d`\n\n", len(order))

	for _, familyName := range order {
		fmt.Fprintf(&builder, "## %s\n\n", familyName)
		builder.WriteString("| Tool | Scope | Risk | Approval | Side Effects | Idempotency |\n")
		builder.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		familyCaps := families[familyName]
		for i := range familyCaps {
			capability := &familyCaps[i]
			approval := "no"
			if capability.RequiresApproval {
				approval = "yes"
			}
			fmt.Fprintf(&builder, "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
				capability.Name,
				capability.Scope,
				capability.RiskLevel,
				approval,
				capability.SideEffects,
				capability.Idempotency)
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
