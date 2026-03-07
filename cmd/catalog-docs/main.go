package main

import (
	"fmt"
	"os"
	"path/filepath"

	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

func main() {
	target := "docs/CAPABILITY_CATALOG.md"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", filepath.Dir(target), err)
		os.Exit(1)
	}

	payload := tooladapter.DefaultCapabilitiesMarkdown()
	if err := os.WriteFile(target, []byte(payload), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", target, err)
		os.Exit(1)
	}

	fmt.Printf("capability catalog generated at %s\n", target)
}
