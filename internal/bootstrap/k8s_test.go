//go:build k8s

package bootstrap

import (
	"testing"

	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestK8sBundle_WithClient(t *testing.T) {
	b := K8sBundle()
	if b.Name != "k8s" {
		t.Fatalf("expected bundle name 'k8s', got %q", b.Name)
	}

	cfg := Config{
		CommandRunner: tooladapter.NewLocalCommandRunner(),
		K8sClient:     k8sfake.NewSimpleClientset(),
		K8sNamespace:  "test-ns",
	}
	handlers := b.Build(cfg)
	if len(handlers) != 12 {
		t.Fatalf("expected 12 k8s handlers, got %d", len(handlers))
	}
}

func TestK8sBundle_NilClient(t *testing.T) {
	b := K8sBundle()
	cfg := Config{
		CommandRunner: tooladapter.NewLocalCommandRunner(),
		K8sClient:     nil,
	}
	handlers := b.Build(cfg)
	if len(handlers) != 0 {
		t.Fatalf("expected 0 handlers with nil client, got %d", len(handlers))
	}
}

func TestK8sBundle_WrongClientType(t *testing.T) {
	b := K8sBundle()
	cfg := Config{
		CommandRunner: tooladapter.NewLocalCommandRunner(),
		K8sClient:     "not-a-k8s-client",
	}
	handlers := b.Build(cfg)
	if len(handlers) != 0 {
		t.Fatalf("expected 0 handlers with wrong client type, got %d", len(handlers))
	}
}

func TestRegistryWithK8s_ProducesAllHandlers(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDefaults()
	registry.Register(K8sBundle())

	cfg := Config{
		CommandRunner: tooladapter.NewLocalCommandRunner(),
		K8sClient:     k8sfake.NewSimpleClientset(),
		K8sNamespace:  "test-ns",
	}
	handlers := registry.Handlers(cfg)

	// 96 default + 12 k8s = 108
	if len(handlers) != 108 {
		t.Fatalf("expected 108 handlers with k8s, got %d", len(handlers))
	}
}
