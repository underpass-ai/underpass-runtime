# Workspace Execution Service (Go)

SERVICE_NAME := workspace
BINARY_NAME := workspace-service
CMD_PATH := ./cmd/workspace
COVERAGE_FILE := coverage.out
COVERAGE_CORE_FILE := coverage-core.out
COVERAGE_FULL_FILE := coverage-full.out
COVERAGE_MIN ?= 76
CORE_TEST_PACKAGES := ./internal/app ./internal/adapters/audit ./internal/adapters/policy ./internal/adapters/sessionstore ./internal/adapters/invocationstore

# Sandbox-safe defaults (override if needed)
export GOCACHE ?= /tmp/go-cache-workspace
export GOTMPDIR ?= /tmp/go-tmp-workspace

.PHONY: help run build test test-all test-core coverage coverage-core coverage-full catalog-docs clean docker-build docker-push

help:
	@echo "Workspace service commands"
	@echo ""
	@echo "  make run                # Run service locally"
	@echo "  make build              # Build binary"
	@echo "  make test               # Run all unit tests"
	@echo "  make coverage           # Full-package coverage report (informational)"
	@echo "  make coverage-core      # Coverage gate for core execution packages"
	@echo "  make catalog-docs       # Regenerate docs/CAPABILITY_CATALOG.md from DefaultCapabilities()"
	@echo "  make docker-build       # Build container image"
	@echo "  make docker-push        # Push container image"
	@echo ""
	@echo "Variables:"
	@echo "  COVERAGE_MIN=$(COVERAGE_MIN)"

run:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go run $(CMD_PATH)

build:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go build -o bin/$(BINARY_NAME) $(CMD_PATH)

test: test-all

test-all:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go test ./...

test-core:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go test $(CORE_TEST_PACKAGES)

coverage:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go test ./... -coverprofile=$(COVERAGE_FILE)
	go tool cover -func=$(COVERAGE_FILE)

coverage-core:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	# Core gate focuses on deterministic unit-test packages.
	go test $(CORE_TEST_PACKAGES) -coverprofile=$(COVERAGE_CORE_FILE)
	@go tool cover -func=$(COVERAGE_CORE_FILE) | tee /tmp/workspace-coverage-core.txt
	@COVERAGE_VALUE=$$(awk '/^total:/ {print $$3}' /tmp/workspace-coverage-core.txt | sed 's/%//'); \
	if awk "BEGIN {exit !($$COVERAGE_VALUE >= $(COVERAGE_MIN))}"; then \
		echo "Coverage gate passed: $$COVERAGE_VALUE% >= $(COVERAGE_MIN)%"; \
	else \
		echo "Coverage gate failed: $$COVERAGE_VALUE% < $(COVERAGE_MIN)%"; \
		exit 1; \
	fi

coverage-full:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	# Full coverage for all packages (best-effort, used by SonarCloud).
	# All tool/adapter/httpapi tests use fakes — no real infra required.
	-go test ./... -coverprofile=$(COVERAGE_FULL_FILE) -covermode=atomic
	@if [ -f "$(COVERAGE_FULL_FILE)" ]; then \
		go tool cover -func=$(COVERAGE_FULL_FILE) | tail -1; \
		echo "Full coverage report generated: $(COVERAGE_FULL_FILE)"; \
	else \
		echo "Warning: $(COVERAGE_FULL_FILE) was not generated"; \
	fi

catalog-docs:
	@mkdir -p "$(GOCACHE)" "$(GOTMPDIR)"
	go run ./cmd/catalog-docs

clean:
	rm -rf bin coverage*.out

docker-build:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	if [ -z "$$BUILDER" ]; then \
		echo "No container builder found (podman or docker)"; \
		exit 1; \
	fi; \
	$$BUILDER build -f Dockerfile -t registry.underpassai.com/underpass-runtime:v0.1.0 -t registry.underpassai.com/underpass-runtime:latest .

docker-push:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	if [ -z "$$BUILDER" ]; then \
		echo "No container builder found (podman or docker)"; \
		exit 1; \
	fi; \
	$$BUILDER push registry.underpassai.com/underpass-runtime:v0.1.0; \
	$$BUILDER push registry.underpassai.com/underpass-runtime:latest
