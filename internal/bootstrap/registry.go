package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	"github.com/underpass-ai/underpass-runtime/internal/app"
)

// Config holds dependencies needed by bundles to construct handlers.
type Config struct {
	CommandRunner app.CommandRunner

	// K8sClient carries the Kubernetes clientset when built with -tags k8s.
	// Typed as any to avoid k8s.io imports in non-k8s builds.
	K8sClient    any
	K8sNamespace string
}

// Bundle is a named group of tool handlers built from a shared Config.
type Bundle struct {
	Name  string
	Build func(cfg Config) []tooladapter.Handler
}

// Registry collects bundles and produces a unified handler set.
type Registry struct {
	bundles  []Bundle
	disabled map[string]bool
}

// NewRegistry creates a registry. Any bundle names in disabled are skipped
// during Handlers().
func NewRegistry(disabled ...string) *Registry {
	dm := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		dm[name] = true
	}
	return &Registry{disabled: dm}
}

// Register adds a bundle to the registry.
func (r *Registry) Register(b Bundle) {
	r.bundles = append(r.bundles, b)
}

// RegisterDefaults registers all non-k8s bundles.
func (r *Registry) RegisterDefaults() {
	r.Register(CoreBundle())
	r.Register(RepoBundle())
	r.Register(SecopsBundle())
	r.Register(MessagingBundle())
	r.Register(DataBundle())
	r.Register(ImageBundle())
}

// Handlers builds all handlers from registered, non-disabled bundles.
func (r *Registry) Handlers(cfg Config) []tooladapter.Handler {
	var all []tooladapter.Handler
	for _, b := range r.bundles {
		if r.disabled[b.Name] {
			continue
		}
		all = append(all, b.Build(cfg)...)
	}
	return all
}

// BuildEngine constructs the tool engine from all registered handlers.
func (r *Registry) BuildEngine(cfg Config) *tooladapter.Engine {
	return tooladapter.NewEngine(r.Handlers(cfg)...)
}
