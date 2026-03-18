REGISTRY ?= ghcr.io/underpass-ai/underpass-runtime
IMAGE_NAME = e2e-multi-agent-pipeline
VERSION ?= v1.0.0
NAMESPACE ?= underpass-runtime
IMAGE = $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
LATEST = $(REGISTRY)/$(IMAGE_NAME):latest

BUILDER := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build push build-push deploy status logs delete

build:
	@echo "Building $(IMAGE)..."
	cd ../../../ && $(BUILDER) build \
		-f e2e/tests/13-multi-agent-pipeline/Dockerfile \
		-t $(IMAGE) \
		-t $(LATEST) \
		.
	@echo "Built $(IMAGE)"

push:
	@echo "Pushing $(IMAGE)..."
	$(BUILDER) push $(IMAGE)
	$(BUILDER) push $(LATEST)
	@echo "Pushed $(IMAGE)"

build-push: build push

deploy:
	kubectl apply -f job.yaml

status:
	kubectl get job -n $(NAMESPACE) e2e-multi-agent-pipeline || echo "Job not found"
	kubectl get pods -n $(NAMESPACE) -l app=e2e-multi-agent-pipeline || echo "No pods found"

logs:
	kubectl logs -n $(NAMESPACE) -l app=e2e-multi-agent-pipeline --tail=200 -f

delete:
	kubectl delete job -n $(NAMESPACE) e2e-multi-agent-pipeline || true
