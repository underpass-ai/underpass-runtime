module github.com/underpass-ai/underpass-runtime/e2e/tests/11-tool-learning-pipeline

go 1.25.0

// This module exists only to prevent the root go.mod from trying to compile
// this E2E test. The actual build happens inside the tool-learning module
// via the Dockerfile (which copies main.go into cmd/e2e-runner/).
