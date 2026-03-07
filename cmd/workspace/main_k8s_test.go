//go:build k8s

package main

import (
	"bytes"
	"log/slog"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"
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
