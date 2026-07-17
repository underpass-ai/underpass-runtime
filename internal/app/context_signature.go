package app

import (
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// DeriveContextSignature computes a context grouping key from the session and
// workspace digest. This key maps to the same format used by the tool-learning
// pipeline: "task_family:lang:constraints_class".
//
// The task-family slot is fixed to "general": the signature describes the
// bandit context (environment + constraints), while the tool is the arm and
// already identifies every policy key and telemetry record on its own.
// Deriving the family from the invoked tool would shard write-side policies
// across keys that the recommend path — which runs before any tool is chosen —
// can never query. The slot is reserved for a task classifier (e.g. from
// ceremony/task metadata) that can feed the write and read paths symmetrically.
func DeriveContextSignature(session domain.Session, digest ContextDigest) string {
	lang := digest.RepoLanguage
	if lang == "" {
		lang = "unknown"
	}
	return "general:" + lang + ":" + classifyConstraints(session)
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
