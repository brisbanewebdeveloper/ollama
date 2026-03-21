IMAGE ?= ollama/ollama:custom

.PHONY: build docker-build

build: docker-build

docker-build:
	docker build -t $(IMAGE) .
