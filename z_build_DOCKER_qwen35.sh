#!/usr/bin/env bash
set -euo pipefail

IMAGE="${IMAGE:-ollama/ollama}"
TAG="${TAG:-main-qwen35}"
VERSION="${VERSION:-0.6.4}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
OUTPUT_MODE="${OUTPUT_MODE:-load}" # load|push
FLAVOR="${FLAVOR:-auto}"           # auto|rocm
DRY_RUN="${DRY_RUN:-0}"
NO_CACHE="${NO_CACHE:-0}"

case "$OUTPUT_MODE" in load|push) ;; *) echo "OUTPUT_MODE must be load|push" >&2; exit 2;; esac
case "$FLAVOR" in auto|rocm) ;; *) echo "FLAVOR must be auto|rocm" >&2; exit 2;; esac

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] && cd "$repo_root"

IFS=',' read -r -a platforms <<<"$PLATFORMS"
(( ${#platforms[@]} > 0 )) || { echo "PLATFORMS is empty" >&2; exit 2; }

if [[ "$FLAVOR" == "rocm" ]] && [[ ${#platforms[@]} -ne 1 || "${platforms[0]}" != "linux/amd64" ]]; then
  echo "FLAVOR=rocm requires PLATFORMS=linux/amd64" >&2
  exit 2
fi

args=(--progress=plain)
[[ "$NO_CACHE" == "1" ]] && args+=(--no-cache)
args+=(--build-arg "VERSION=$VERSION")
[[ "$FLAVOR" != "auto" ]] && args+=(--build-arg "FLAVOR=$FLAVOR")

run() {
  echo "+ $*" >&2
  [[ "$DRY_RUN" == "1" ]] || "$@"
}

if [[ "$OUTPUT_MODE" == "push" ]]; then
  run docker buildx build --platform "$PLATFORMS" --push -t "$IMAGE:$TAG" "${args[@]}" .
  exit 0
fi

# buildx can't --load multi-platform images; load per-arch tags instead
if (( ${#platforms[@]} > 1 )); then
  for p in "${platforms[@]}"; do
    arch="${p#*/}"
    run docker buildx build --platform "$p" --load -t "$IMAGE:$TAG-$arch" "${args[@]}" .
  done
  exit 0
fi

run docker buildx build --platform "${platforms[0]}" --load -t "$IMAGE:$TAG" "${args[@]}" .
