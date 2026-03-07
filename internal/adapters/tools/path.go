package tools

import (
	"path/filepath"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func resolvePath(session domain.Session, relativePath string) (string, *domain.Error) {
	if relativePath == "" {
		relativePath = "."
	}

	cleaned := filepath.Clean(relativePath)
	if strings.HasPrefix(cleaned, "..") {
		return "", &domain.Error{Code: "policy_denied", Message: "path escapes workspace", Retryable: false}
	}

	if !pathAllowed(cleaned, session.AllowedPaths) {
		return "", &domain.Error{Code: "policy_denied", Message: "path outside allowed_paths", Retryable: false}
	}

	resolved := filepath.Join(session.WorkspacePath, cleaned)
	workspaceClean := filepath.Clean(session.WorkspacePath)
	resolvedClean := filepath.Clean(resolved)
	if resolvedClean != workspaceClean && !strings.HasPrefix(resolvedClean, workspaceClean+string(filepath.Separator)) {
		return "", &domain.Error{Code: "policy_denied", Message: "path escapes workspace", Retryable: false}
	}

	return resolvedClean, nil
}

func pathAllowed(path string, allowlist []string) bool {
	if len(allowlist) == 0 {
		allowlist = []string{"."}
	}
	cleanPath := filepath.Clean(path)
	for _, allowed := range allowlist {
		cleanAllowed := filepath.Clean(allowed)
		if cleanAllowed == "." {
			if !strings.HasPrefix(cleanPath, "..") {
				return true
			}
			continue
		}
		if cleanPath == cleanAllowed || strings.HasPrefix(cleanPath, cleanAllowed+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
