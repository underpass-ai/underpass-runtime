REGISTRY ?= ghcr.io/underpass-ai/underpass-runtime
VERSION  ?= v1.0.0

PROFILES = base toolchains secops container k6 fat

CONTAINER_TOOL := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: all build push clean $(addprefix build-,$(PROFILES)) $(addprefix push-,$(PROFILES))

all: build

build: $(addprefix build-,$(PROFILES))

push: $(addprefix push-,$(PROFILES))

build-%:
	$(CONTAINER_TOOL) build \
		--target $* \
		-t $(REGISTRY)/workspace-runner-$*:$(VERSION) \
		-t $(REGISTRY)/workspace-runner-$*:latest \
		-f Dockerfile \
		.

push-%: build-%
	$(CONTAINER_TOOL) push $(REGISTRY)/workspace-runner-$*:$(VERSION)
	$(CONTAINER_TOOL) push $(REGISTRY)/workspace-runner-$*:latest

clean:
	@for p in $(PROFILES); do \
		$(CONTAINER_TOOL) rmi $(REGISTRY)/workspace-runner-$$p:$(VERSION) 2>/dev/null || true; \
		$(CONTAINER_TOOL) rmi $(REGISTRY)/workspace-runner-$$p:latest 2>/dev/null || true; \
	done

list:
	@for p in $(PROFILES); do \
		echo "$(REGISTRY)/workspace-runner-$$p:$(VERSION)"; \
	done

bundles-json:
	@printf '{'
	@first=true; for p in $(PROFILES); do \
		if [ "$$first" = true ]; then first=false; else printf ','; fi; \
		printf '"'"$$p"'":"$(REGISTRY)/workspace-runner-'"$$p"':$(VERSION)"'; \
	done
	@printf '}\n'
