package app

import (
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestClassifyTaskFamily(t *testing.T) {
	cases := []struct {
		tool   string
		family string
	}{
		{"fs.list", "io"},
		{"fs.write_file", "io"},
		{"git.status", "vcs"},
		{"git.push", "vcs"},
		{"docker.build", "build"},
		{"k8s.apply", "deploy"},
		{"api.call", "network"},
		{"mongo.query", "data"},
		{"shell.exec", "exec"},
		{"test.run", "quality"},
		{"unknown.tool", "general"},
	}
	for _, tc := range cases {
		got := classifyTaskFamily(tc.tool)
		if got != tc.family {
			t.Errorf("classifyTaskFamily(%q) = %q, want %q", tc.tool, got, tc.family)
		}
	}
}

func TestDeriveContextSignature(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{RepoLanguage: "go"}

	sig := DeriveContextSignature(session, "fs.write_file", digest)
	if sig != "io:go:standard" {
		t.Errorf("got %q, want io:go:standard", sig)
	}
}

func TestDeriveContextSignature_HighConstraints(t *testing.T) {
	session := domain.Session{
		AllowedPaths: []string{"/src"},
		Principal:    domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{RepoLanguage: "python"}

	sig := DeriveContextSignature(session, "git.push", digest)
	if sig != "vcs:python:constraints_high" {
		t.Errorf("got %q, want vcs:python:constraints_high", sig)
	}
}

func TestDeriveContextSignature_UnknownLanguage(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{}

	sig := DeriveContextSignature(session, "docker.build", digest)
	if sig != "build:unknown:standard" {
		t.Errorf("got %q, want build:unknown:standard", sig)
	}
}
