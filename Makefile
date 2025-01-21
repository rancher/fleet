REPO ?= rancher
IMAGE = $(REPO)/fleet:$(TAG)
ARCH ?= $(shell docker info --format '{{.ClientInfo.Arch}}')

RUNNER := docker
IMAGE_BUILDER := $(RUNNER) buildx
MACHINE := rancher
BUILDX_ARGS ?= --sbom=true --attest type=provenance,mode=max
IMAGE_ARGS ?= --build-arg=ARCH=$(ARCH) --build-arg=BUILD_ENV=goreleaser
DEFAULT_PLATFORMS := linux/amd64,linux/arm64
# Define target platforms, image builder and the fully qualified image name.
TARGET_PLATFORMS ?= linux/amd64,linux/arm64

buildx-machine: ## create rancher dockerbuildx machine targeting platform defined by DEFAULT_PLATFORMS.
	@docker buildx ls | grep $(MACHINE) || \
		docker buildx create --name=$(MACHINE) --platform=$(DEFAULT_PLATFORMS)

.PHONY: push-image
push-image: buildx-machine ## build the container image targeting all platforms defined by TARGET_PLATFORMS and push to a registry.
	$(IMAGE_BUILDER) build -f package/Dockerfile \
		--builder $(MACHINE) $(IMAGE_ARGS) $(IID_FILE_FLAG) $(BUILDX_ARGS) \
		--build-arg VERSION=$(VERSION) --platform=$(TARGET_PLATFORMS) -t "$(REPO)/fleet:$(TAG)" --push .
	@echo "Pushed $(REPO)/fleet:$(TAG)"

.PHONY: push-image-agent
push-image-agent: buildx-machine ## build the container image targeting all platforms defined by TARGET_PLATFORMS and push to a registry.
	$(IMAGE_BUILDER) build -f package/Dockerfile.agent \
		--builder $(MACHINE) $(IMAGE_ARGS) $(IID_FILE_FLAG) $(BUILDX_ARGS) \
		--build-arg VERSION=$(VERSION) --platform=$(TARGET_PLATFORMS) -t "$(REPO)/fleet-agent:$(TAG)" --push .
	@echo "Pushed $(REPO)/fleet-agent:$(TAG)"