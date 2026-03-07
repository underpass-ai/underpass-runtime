//go:build !k8s

package main

import "testing"

func TestInitKubernetesRuntime_K8sBackendWithoutTag(t *testing.T) {
	_, err := initKubernetesRuntime("kubernetes")
	if err == nil {
		t.Fatal("expected error for kubernetes backend without build tag")
	}
}

func TestBuildWorkspaceManagerUnsupportedBackendNoK8sTag(t *testing.T) {
	_, err := buildWorkspaceManager("postgres", t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}
