package bootstrap

import (
	"testing"

	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

func localConfig() Config {
	return Config{
		CommandRunner: tooladapter.NewLocalCommandRunner(),
	}
}

func TestRegistryDefaults_ProducesAllHandlers(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDefaults()

	handlers := registry.Handlers(localConfig())

	// Expected handler count across 9 default bundles:
	// core=27, repo=33, secops=7, messaging=9, data=9, image=10, github=4, observability=1 = 100
	if len(handlers) != 100 {
		t.Fatalf("expected 100 default handlers, got %d", len(handlers))
	}
}

func TestRegistryDefaults_NoDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDefaults()

	handlers := registry.Handlers(localConfig())
	seen := make(map[string]bool, len(handlers))
	for _, h := range handlers {
		if seen[h.Name()] {
			t.Fatalf("duplicate handler name: %s", h.Name())
		}
		seen[h.Name()] = true
	}
}

func TestRegistryDisable_ExcludesBundle(t *testing.T) {
	registry := NewRegistry("messaging", "data")
	registry.RegisterDefaults()

	handlers := registry.Handlers(localConfig())

	// Without messaging (9) and data (9) = 100 - 18 = 82
	if len(handlers) != 82 {
		t.Fatalf("expected 82 handlers with messaging+data disabled, got %d", len(handlers))
	}

	for _, h := range handlers {
		switch h.Name() {
		case "nats.request", "kafka.consume", "redis.get", "mongo.find":
			t.Fatalf("disabled bundle handler %q should not be present", h.Name())
		}
	}
}

func TestRegistryDisableAll_ProducesZeroHandlers(t *testing.T) {
	registry := NewRegistry("core", "repo", "secops", "messaging", "data", "image", "docker", "github", "observability")
	registry.RegisterDefaults()

	handlers := registry.Handlers(localConfig())
	if len(handlers) != 0 {
		t.Fatalf("expected 0 handlers with all disabled, got %d", len(handlers))
	}
}

func TestRegistryCustomBundle(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Bundle{
		Name: "custom",
		Build: func(_ Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewFSStatHandler(nil),
			}
		},
	})

	handlers := registry.Handlers(localConfig())
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	if handlers[0].Name() != "fs.stat" {
		t.Fatalf("expected fs.stat, got %s", handlers[0].Name())
	}
}

func TestRegistryBuildEngine_CanInvoke(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDefaults()

	engine := registry.BuildEngine(localConfig())
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBundleCounts(t *testing.T) {
	cfg := localConfig()
	tests := []struct {
		name  string
		b     Bundle
		count int
	}{
		{"core", CoreBundle(), 27},
		{"repo", RepoBundle(), 33},
		{"secops", SecopsBundle(), 7},
		{"messaging", MessagingBundle(), 9},
		{"data", DataBundle(), 9},
		{"image", ImageBundle(), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers := tt.b.Build(cfg)
			if len(handlers) != tt.count {
				t.Fatalf("%s: expected %d handlers, got %d", tt.name, tt.count, len(handlers))
			}
		})
	}
}

func TestBundleNames(t *testing.T) {
	tests := []struct {
		b    Bundle
		name string
	}{
		{CoreBundle(), "core"},
		{RepoBundle(), "repo"},
		{SecopsBundle(), "secops"},
		{MessagingBundle(), "messaging"},
		{DataBundle(), "data"},
		{ImageBundle(), "image"},
		{DockerBundle(), "docker"},
	}
	for _, tt := range tests {
		if tt.b.Name != tt.name {
			t.Fatalf("expected bundle name %q, got %q", tt.name, tt.b.Name)
		}
	}
}

func TestDockerBundle_NilClient(t *testing.T) {
	b := DockerBundle()
	handlers := b.Build(localConfig())
	if handlers != nil {
		t.Fatalf("expected nil handlers for nil DockerClient, got %d", len(handlers))
	}
}

func TestNewRegistryEmpty(t *testing.T) {
	registry := NewRegistry()
	handlers := registry.Handlers(localConfig())
	if len(handlers) != 0 {
		t.Fatalf("expected 0 handlers from empty registry, got %d", len(handlers))
	}
}
