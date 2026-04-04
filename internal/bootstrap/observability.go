package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// ObservabilityBundle returns tools for querying observability systems.
func ObservabilityBundle() Bundle {
	return Bundle{
		Name: "observability",
		Build: func(_ Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewPrometheusQueryHandler(),
			}
		},
	}
}
