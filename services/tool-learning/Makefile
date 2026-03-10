SERVICE_NAME := tool-learning
BINARY_NAME := tool-learning
CMD_PATH := ./cmd/tool-learning

REGISTRY ?= ghcr.io/underpass-ai/underpass-runtime
VERSION  ?= latest

.PHONY: help build test coverage clean docker-build docker-push

help:
	@echo "Tool Learning Service commands"
	@echo ""
	@echo "  make build          # Build binary"
	@echo "  make test           # Run all tests"
	@echo "  make coverage       # Coverage report"
	@echo "  make docker-build   # Build container image"
	@echo "  make docker-push    # Push container image"

build:
	go build -o bin/$(BINARY_NAME) $(CMD_PATH)

test:
	go test ./...

coverage:
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out

clean:
	rm -rf bin coverage.out

docker-build:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	$$BUILDER build -f Dockerfile -t $(REGISTRY)/$(SERVICE_NAME):$(VERSION) .

docker-push:
	@BUILDER=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	$$BUILDER push $(REGISTRY)/$(SERVICE_NAME):$(VERSION)
