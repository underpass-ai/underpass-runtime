REGISTRY ?= ghcr.io/underpass-ai/underpass-runtime
IMAGE_NAME = e2e-full-infra-stack
VERSION ?= v1.0.0
IMAGE = $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
LATEST = $(REGISTRY)/$(IMAGE_NAME):latest
BUILDER := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build push build-push deploy status logs delete

build:
	cd ../../../ && $(BUILDER) build -f e2e/tests/14-full-infra-stack/Dockerfile -t $(IMAGE) -t $(LATEST) .

push:
	$(BUILDER) push $(IMAGE)
	$(BUILDER) push $(LATEST)

build-push: build push

deploy:
	kubectl apply -f job.yaml

status:
	kubectl get pods -n underpass-runtime -l app=e2e-full-infra-stack

logs:
	kubectl logs -n underpass-runtime -l app=e2e-full-infra-stack --tail=200

delete:
	kubectl delete job -n underpass-runtime e2e-full-infra-stack || true
