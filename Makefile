IMAGE ?= ollama/ollama:custom
DOCKERFILE ?= Dockerfile.custom
VERSION ?= $(shell git describe --tags --first-parent --abbrev=7 --long --dirty --always 2>/dev/null | sed -e 's/^v//g')
GOFLAGS ?= '-ldflags=-w -s "-X=github.com/ollama/ollama/version.Version=$(VERSION)" "-X=github.com/ollama/ollama/server.mode=release"'

.PHONY: build docker-build

build:
	@echo "It is going to take about 20 minutes"
	@$(MAKE) docker-build

docker-build:
	docker build -f $(DOCKERFILE) -t $(IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GOFLAGS=$(GOFLAGS) \
		.
