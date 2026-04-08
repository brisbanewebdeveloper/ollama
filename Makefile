IMAGE ?= ollama/ollama:custom
DOCKERFILE ?= Dockerfile.custom
# BUILD_PROFILE values: cuda-12, cuda-13, all
BUILD_PROFILE ?= cuda-13
TARGET_STAGE ?= runtime-$(BUILD_PROFILE)
VERSION ?= $(shell git describe --tags --first-parent --abbrev=7 --long --dirty --always 2>/dev/null | sed -e 's/^v//g')

.PHONY: build docker-build

build:
	@echo "Building $(IMAGE) with profile $(BUILD_PROFILE)"
	@echo "It is going to take about 20 minutes or less"
	@sleep 5
	@$(MAKE) docker-build

docker-build:
	docker build -f $(DOCKERFILE) -t $(IMAGE) \
		--target $(TARGET_STAGE) \
		--build-arg VERSION=$(VERSION) \
		.
