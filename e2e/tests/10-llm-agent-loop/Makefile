REGISTRY   ?= registry.underpassai.com/underpass-runtime
IMAGE_NAME  = e2e-llm-agent-loop
VERSION    ?= v1.0.0
NAMESPACE  ?= underpass-runtime

CONTAINER_TOOL := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build push deploy status logs delete run-claude run-openai run-vllm

build:
	cd ../../../ && $(CONTAINER_TOOL) build \
		-t $(REGISTRY)/$(IMAGE_NAME):$(VERSION) \
		-f e2e/tests/10-llm-agent-loop/Dockerfile \
		.

push: build
	$(CONTAINER_TOOL) push $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

deploy:
	kubectl apply -n $(NAMESPACE) -f job.yaml

# Deploy with specific LLM provider
run-claude:
	sed 's/value: "claude"/value: "claude"/' job.yaml | \
		kubectl apply -n $(NAMESPACE) -f -

run-openai:
	sed 's/value: "claude"/value: "openai"/' job.yaml | \
		kubectl apply -n $(NAMESPACE) -f -

run-vllm:
	sed 's/value: "claude"/value: "vllm"/' job.yaml | \
		kubectl apply -n $(NAMESPACE) -f -

status:
	kubectl get job -n $(NAMESPACE) $(IMAGE_NAME) || echo "Job not found"
	kubectl get pods -n $(NAMESPACE) -l app=$(IMAGE_NAME) || echo "No pods found"

logs:
	kubectl logs -n $(NAMESPACE) -l app=$(IMAGE_NAME) --tail=100 -f

delete:
	kubectl delete job -n $(NAMESPACE) $(IMAGE_NAME) || true
