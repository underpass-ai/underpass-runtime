package app

import (
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestDeriveContextSignature(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{RepoLanguage: "go"}

	sig := DeriveContextSignature(session, digest)
	if sig != "general:go:standard" {
		t.Errorf("got %q, want general:go:standard", sig)
	}
}

func TestDeriveContextSignature_HighConstraints(t *testing.T) {
	session := domain.Session{
		AllowedPaths: []string{"/src"},
		Principal:    domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{RepoLanguage: "python"}

	sig := DeriveContextSignature(session, digest)
	if sig != "general:python:constraints_high" {
		t.Errorf("got %q, want general:python:constraints_high", sig)
	}
}

func TestDeriveContextSignature_LowConstraints(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{Roles: []string{"platform_admin"}},
	}
	digest := ContextDigest{RepoLanguage: "go"}

	sig := DeriveContextSignature(session, digest)
	if sig != "general:go:constraints_low" {
		t.Errorf("got %q, want general:go:constraints_low", sig)
	}
}

func TestDeriveContextSignature_UnknownLanguage(t *testing.T) {
	session := domain.Session{
		Principal: domain.Principal{Roles: []string{"developer"}},
	}
	digest := ContextDigest{}

	sig := DeriveContextSignature(session, digest)
	if sig != "general:unknown:standard" {
		t.Errorf("got %q, want general:unknown:standard", sig)
	}
}
