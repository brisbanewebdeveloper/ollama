# Updates

## How to build the Docker image for the `custom` branch

This creates a Docker image tagged `ollama/ollama:custom` using `Dockerfile.custom`, which skips the MLX build stage while keeping the standard CPU, CUDA, and Vulkan runtime artifacts:

```
make
```

## 2026-03-21

- Branch: `custom`
- Source: derived from `git status --short` and `git diff --stat -- . ':(exclude).history/**'`
- Branch state: the current `custom` changes are in the working tree rather than in commits ahead of `main`

### Changes

- Added Modelfile-level `think` support so models can set a default `PARAMETER think` value such as `false`, `low`, `medium`, or `high`
- Persisted model-level `think` defaults, surfaced them in generated Modelfiles and show output, and applied them when chat or generate requests omit `think`
- Added tests covering API parameter parsing, Modelfile creation, create and show behavior, and chat default precedence
- Updated the thinking and Modelfile documentation to describe model-level `think` defaults
- Added a root `Makefile` with a Docker image build target for `ollama/ollama:custom`
- Added `Dockerfile.custom` and updated the root `Makefile` to use it by default so branch-local Docker builds skip the MLX stage
- Updated the root `Makefile` so the default `make` command prints `It is going to take about 20 minutes` before starting the Docker build
- Updated the root `Makefile` to pass a git-derived `VERSION` into the custom Docker build so `make` no longer produces an Ollama server that reports version `0.0.0`
- Updated `Dockerfile.custom` to apply release ldflags directly from `VERSION` during `go build`, fixing the failed custom `make` build caused by invalid `GOFLAGS` handling
- Updated `Dockerfile.custom` to normalize hash-only or `0.0.0` build versions to a semver fallback such as `0.6.4+custom.g<hash>` so `/api/version` stays compatible with VS Code integrations

### Affected Files

- `api/types.go`
- `api/types_test.go`
- `server/images.go`
- `server/routes.go`
- `server/routes_create_test.go`
- `server/routes_generate_test.go`
- `parser/parser_test.go`
- `docs/modelfile.mdx`
- `docs/capabilities/thinking.mdx`
- `Makefile`
- `Dockerfile.custom`

### Links

- None recorded
