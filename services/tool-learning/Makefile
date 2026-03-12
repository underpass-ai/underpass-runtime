SERVICE_NAME := tool-learning
BINARY_NAME := tool-learning
CMD_PATH := ./cmd/tool-learning

REGISTRY ?= ghcr.io/underpass-ai/underpass-runtime
VERSION  ?= latest
GOLANGCI_LINT_VERSION ?= v2.11

CORE_PKGS := ./internal/domain ./internal/app ./internal/adapters/...

.PHONY: help build test integration-test lint vet coverage coverage-core clean docker-build docker-push demo

help:
	@echo "Tool Learning Service commands"
	@echo ""
	@echo "  make build             # Build binary"
	@echo "  make test              # Run all tests with race detector"
	@echo "  make integration-test  # Run integration tests (embedded services)"
	@echo "  make lint              # Run golangci-lint + go vet"
	@echo "  make vet               # Run go vet"
	@echo "  make coverage          # Full coverage report (for SonarCloud)"
	@echo "  make coverage-core     # Core coverage gate (80% — domain + app + adapters)"
	@echo "  make demo              # Run zero-infra demo (Thompson Sampling live)"
	@echo "  make docker-build      # Build container image"
	@echo "  make docker-push       # Push container image"

build:
	go build -o bin/$(BINARY_NAME) $(CMD_PATH)

test:
	go test -race -count=1 ./...

integration-test:
	go test -race -count=1 -tags integration -v ./internal/integration/...

lint: vet
	golangci-lint run --timeout=5m

vet:
	go vet ./...

coverage:
	go test ./... -coverprofile=coverage-full.out -covermode=atomic
	go tool cover -func=coverage-full.out

coverage-core:
	go test $(CORE_PKGS) -coverprofile=coverage-core.out -covermode=atomic
	@COVERAGE=$$(go tool cover -func=coverage-core.out | awk '/^total:/ {print $$3}' | sed 's/%//'); \
	echo "Core coverage: $${COVERAGE}%"; \
	if awk "BEGIN {exit !($${COVERAGE} >= 80)}"; then \
		echo "Coverage gate PASSED: $${COVERAGE}% >= 80%"; \
	else \
		echo "Coverage gate FAILED: $${COVERAGE}% < 80%"; exit 1; \
	fi

demo:
	@go run ./cmd/demo --hours=24 --per-hour=200 --max-error-rate=0.10 --max-p95-latency-ms=3000

clean:
	rm -rf bin coverage.out coverage-full.out coverage-core.out

docker-build:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	$$BUILDER build -f Dockerfile -t $(REGISTRY)/$(SERVICE_NAME):$(VERSION) .

docker-push:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	$$BUILDER push $(REGISTRY)/$(SERVICE_NAME):$(VERSION)
