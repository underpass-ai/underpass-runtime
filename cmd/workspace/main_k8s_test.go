//go:build k8s

package main

import (
	"bytes"
	"log/slog"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestBuildWorkspaceManagerKubernetes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	kubeClient := k8sfake.NewSimpleClientset()
	k8s := &k8sRuntime{client: kubeClient}
	manager, err := buildWorkspaceManager("kubernetes", t.TempDir(), k8s, memStore)
	if err != nil {
		t.Fatalf("unexpected kubernetes workspace manager error: %v", err)
	}
	if manager == nil {
		t.Fatal("expected non-nil kubernetes workspace manager")
	}
}

func TestInitKubernetesRuntime_NonK8sBackend(t *testing.T) {
	rt, err := initKubernetesRuntime("local")
	if err != nil {
		t.Fatalf("unexpected error for non-k8s backend: %v", err)
	}
	if rt != nil {
		t.Fatal("expected nil runtime for local backend")
	}
}

func TestBuildWorkspaceManagerKubernetes_NilClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err = buildWorkspaceManager("kubernetes", t.TempDir(), nil, memStore)
	if err == nil {
		t.Fatal("expected error for nil k8s runtime")
	}
}

func TestBuildWorkspaceManagerKubernetes_UnsupportedBackend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err = buildWorkspaceManager("unsupported", t.TempDir(), nil, memStore)
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestK8sToolHandlers_NilRuntime(t *testing.T) {
	handlers := k8sToolHandlers(nil, nil, "default")
	if handlers != nil {
		t.Fatalf("expected nil handlers for nil k8s runtime, got %d", len(handlers))
	}
}

func TestK8sToolHandlers_NilClient(t *testing.T) {
	handlers := k8sToolHandlers(nil, &k8sRuntime{client: nil}, "default")
	if handlers != nil {
		t.Fatalf("expected nil handlers for nil client, got %d", len(handlers))
	}
}

func TestK8sToolHandlers_WithClient(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handlers := k8sToolHandlers(nil, &k8sRuntime{client: client}, "test-ns")
	if len(handlers) == 0 {
		t.Fatal("expected non-empty handlers list")
	}
}

func TestStartPodJanitorIfEnabled_NonK8sBackend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cancel := startPodJanitorIfEnabled("local", "default", nil, nil, logger)
	if cancel != nil {
		t.Fatal("expected nil cancel for non-k8s backend")
	}
}

func TestStartPodJanitorIfEnabled_Disabled(t *testing.T) {
	t.Setenv("WORKSPACE_K8S_POD_JANITOR_ENABLED", "false")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	client := k8sfake.NewSimpleClientset()
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	cancel := startPodJanitorIfEnabled("kubernetes", "default", &k8sRuntime{client: client}, memStore, logger)
	if cancel != nil {
		t.Fatal("expected nil cancel when janitor disabled")
	}
}

func TestStartPodJanitorIfEnabled_Enabled(t *testing.T) {
	t.Setenv("WORKSPACE_K8S_POD_JANITOR_ENABLED", "true")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	client := k8sfake.NewSimpleClientset()
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	cancel := startPodJanitorIfEnabled("kubernetes", "test-ns", &k8sRuntime{client: client}, memStore, logger)
	if cancel == nil {
		t.Fatal("expected non-nil cancel when janitor enabled")
	}
	cancel()
}

func TestBuildCommandRunnerKubernetes_WithValidConfig(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	runner, err := buildCommandRunner("kubernetes", &k8sRuntime{
		client: client,
		config: &rest.Config{Host: "https://k8s.local"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner == nil {
		t.Fatal("expected non-nil routing runner")
	}
}

func TestBuildCommandRunnerKubernetes(t *testing.T) {
	// kubernetes backend with nil k8s runtime returns error
	_, err := buildCommandRunner("kubernetes", nil)
	if err == nil {
		t.Fatal("expected error for kubernetes runner with nil runtime")
	}

	// kubernetes backend with fake client but nil config returns error
	kubeClient := k8sfake.NewSimpleClientset()
	_, err = buildCommandRunner("kubernetes", &k8sRuntime{client: kubeClient})
	if err == nil {
		t.Fatal("expected error for kubernetes runner with nil rest config")
	}
}
