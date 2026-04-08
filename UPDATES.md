# Updates

## How to build the Docker image for the `custom` branch

This creates a Docker image tagged `ollama/ollama:custom` using `Dockerfile.custom`.
By default it now builds the CPU and CUDA 13 runtime only, which matches the local RTX 4090 server and avoids compiling redundant CUDA 12, Vulkan, and other runtime targets.

```
make
```

To override the profile:

```sh
make BUILD_PROFILE=cuda-12
make BUILD_PROFILE=all
```

## 2026-04-08

### Changes

- Narrowed the custom Docker build so `make` defaults to the `runtime-cuda-13` stage instead of compiling the combined runtime image with CUDA 12, CUDA 13, and Vulkan artifacts
- Added explicit `runtime-cuda-12`, `runtime-cuda-13`, and `runtime-all` Docker stages so `BUILD_PROFILE` can select a single CUDA runtime or the old combined build when needed
- Chose CUDA 13 as the default profile for this custom branch after checking the local server GPU with `nvidia-smi`: RTX 4090, driver `590.48.01`, compute capability `8.9`

### Verification

- `nvidia-smi --query-gpu=name,driver_version,compute_cap --format=csv,noheader`

### Affected Files

- `Makefile`
- `Dockerfile.custom`
- `UPDATES.md`

## 2026-04-01

### Changes

- Resolved the active merge conflict in `cmd/cmd_test.go` by keeping both the CLI `think` override regression tests and the explicit `:cloud` stub normalization tests
- Aligned the conflicted `showInfo` min-version expectation with the current `requires` output and preserved the whitespace-normalized assertion used in that test

### Verification

- `go test ./cmd -run 'TestShowInfo/min version|TestRunHandler_(OmitsImplicitThinkOverride|PreservesExplicitThinkOverride|ExplicitCloudStubMissing_PullsNormalizedNameTEMP|ExplicitCloudStubPresent_SkipsPullTEMP|ExplicitCloudStubPullFailure_IsBestEffortTEMP)$'`

### Affected Files

- `cmd/cmd_test.go`
- `UPDATES.md`

## 2026-03-22

### Changes

- Fixed `ollama run` so it no longer auto-sends `think=true` when the user does not pass `--think`, which had been overriding model-level `PARAMETER think` defaults such as `false`
- Added CLI regression tests covering the implicit `think` omission and the explicit `--think=false` override

### Affected Files

- `cmd/cmd.go`
- `cmd/cmd_test.go`

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
