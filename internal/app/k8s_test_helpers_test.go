//go:build !k8s

package app_test

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

func k8sToolHandlers() []tooladapter.Handler {
	return nil
}
