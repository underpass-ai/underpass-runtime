REGISTRY ?= registry.underpassai.com/underpass-runtime
IMAGE_NAME = e2e-invoke-basic
VERSION ?= v1.0.0
NAMESPACE ?= underpass-runtime
IMAGE = $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
LATEST = $(REGISTRY)/$(IMAGE_NAME):latest

BUILDER := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build push build-push deploy status logs delete help

build:
	@echo "Building $(IMAGE)..."
	cd ../../../ && $(BUILDER) build \
		-f e2e/tests/05-invoke-basic/Dockerfile \
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
	@echo "Deploying E2E test job..."
	kubectl apply -f job.yaml
	@echo "Job deployed"

status:
	kubectl get job -n $(NAMESPACE) e2e-invoke-basic || echo "Job not found"
	kubectl get pods -n $(NAMESPACE) -l app=e2e-invoke-basic || echo "No pods found"

logs:
	kubectl logs -n $(NAMESPACE) -l app=e2e-invoke-basic --tail=100 -f

delete:
	kubectl delete job -n $(NAMESPACE) e2e-invoke-basic || true

help:
	@echo "Available targets:"
	@echo "  build      - Build the container image"
	@echo "  push       - Push the image to registry"
	@echo "  build-push - Build and push"
	@echo "  deploy     - Deploy the test job"
	@echo "  status     - Check job status"
	@echo "  logs       - View test logs"
	@echo "  delete     - Delete test job"
