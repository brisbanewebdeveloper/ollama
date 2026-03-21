IMAGE ?= ollama/ollama:custom
DOCKERFILE ?= Dockerfile.custom

.PHONY: build docker-build

build:
	@echo "It is going to take about 20 minutes"
	@$(MAKE) docker-build

docker-build:
	docker build -f $(DOCKERFILE) -t $(IMAGE) .
