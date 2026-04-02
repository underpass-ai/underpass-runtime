package app

import (
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// DeriveContextSignature computes a context grouping key from the session,
// tool name, and workspace digest. This key maps to the same format used
// by the tool-learning pipeline: "task_family:lang:constraints_class".
func DeriveContextSignature(session domain.Session, toolName string, digest ContextDigest) string {
	family := classifyTaskFamily(toolName)
	lang := digest.RepoLanguage
	if lang == "" {
		lang = "unknown"
	}
	constraints := classifyConstraints(session)
	return family + ":" + lang + ":" + constraints
}

// classifyTaskFamily maps a tool name prefix to a canonical task family.
// Mirrors the families used in tool-learning's context_signature.go.
func classifyTaskFamily(toolName string) string {
	prefix, _, _ := strings.Cut(toolName, ".")
	switch prefix {
	case "fs":
		return "io"
	case "git":
		return "vcs"
	case "docker", "container":
		return "build"
	case "k8s", "kubectl":
		return "deploy"
	case "api", "http":
		return "network"
	case "db", "mongo", "postgres", "sql":
		return "data"
	case "shell", "exec":
		return "exec"
	case "test", "lint", "check":
		return "quality"
	default:
		return "general"
	}
}

// classifyConstraints derives a constraints class from the session metadata.
// The default is "standard". Sessions with security-sensitive metadata or
// restricted allowed_paths get "high"; lightweight sessions get "low".
func classifyConstraints(session domain.Session) string {
	if len(session.AllowedPaths) > 0 {
		return "constraints_high"
	}
	for _, role := range session.Principal.Roles {
		if role == "platform_admin" {
			return "constraints_low"
		}
	}
	return "standard"
}
