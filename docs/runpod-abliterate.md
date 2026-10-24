# Process a model on RunPod and import it into Ollama

The launcher provisions a disposable GPU Pod, downloads original Hugging Face safetensors, runs Heretic, exports the selected modification, converts it to GGUF, downloads verified artifacts, and imports them through the local Ollama API. It does not publish the model to a public registry.

## Requirements

- Python 3.10 or newer on the local machine. The launcher uses the standard library.
- A reachable Ollama server supporting GGUF blob uploads and model creation.
- A funded RunPod account with permission to create and terminate Pods.
- Access to the original model and acceptance of its license or gated-access terms.
- Local free disk space for downloaded GGUFs and Ollama's separate blob storage.

The disposable worker installs Heretic 1.4.0 and its dependencies, conversion dependencies, and llama.cpp build tools. The llama.cpp source revision is pinned. The container image and transitive Python dependencies are not fully locked; review the image and dependency sources before authorizing a paid run. Actual dependency resolution is recorded in the artifacts.

## Processing parameters

The model parameter must be a complete Hugging Face repository ID. A family name such as “Qwen 27B” is insufficient. Choose the original publisher's unquantized safetensors repository, rather than a community GGUF or previously abliterated model. The requested source revision is resolved to an immutable commit before provisioning.

For a dense 27B model, the intended default is one A100 80 GB, at least 96 GB of host RAM, and 300 GB of temporary disk. These requirements can restrict availability. Model architectures must be supported by Heretic, Transformers, the pinned converter, and the destination Ollama version. A model being downloadable does not establish that all four support it.

The search evaluates refusal counts and KL divergence relative to the original model. Automatic selection requires improved refusal counts, a configured maximum refusal fraction, and a configured KL-divergence ceiling. If no trial qualifies, processing fails instead of importing an arbitrary candidate. These metrics do not establish general coding quality, tool-call correctness, or absence of backdoors.

The final standard quantization can be Q4_K_M, Q5_K_M, Q6_K, or Q8_0. Importance-matrix generation is outside this workflow. Vision models require a successfully converted projector; the importer must include both GGUFs. Processing fails if projector conversion fails.

## Paid execution and cleanup

Preview mode must not contact external APIs, provision resources, or import a model. Paid execution requires an explicit execution option. The launcher does not enforce an hourly price cap; confirm current GPU pricing before execution. Runtime limits are enforced by the local launcher and are not a provider-side spending cap.

Keep the launcher running. Normal success, handled failures, runtime expiry, and keyboard interruption should all trigger Pod termination. A machine shutdown, forced process kill, or loss of API access can prevent cleanup and leave a billable Pod running. Check the RunPod console after an interrupted session. The local job state records the Pod identity for manual cleanup.

The paid Pod should be terminated as soon as artifacts have been downloaded and verified, before local Ollama import. An import failure must not keep a GPU running. Keep downloaded files and state so an import can be retried without paying for another processing run.

## Authentication and artifacts

RunPod credentials remain on the local launcher. An optional Hugging Face read credential is sent only to the worker for private or gated downloads. The worker receives a separate generated credential protecting its status and artifact endpoints. Do not place production credentials or project files in the processing environment.

Artifact downloads retry transient failures, retain partial files, use bounded byte ranges, and verify whole-file SHA256. This detects transfer corruption; it does not establish that a malicious worker or source model is trustworthy. Artifacts include GGUFs, trial-selection metrics, provenance, dependency versions, and the processing log. Protect the job directory and do not commit it to Git.

Ollama import streams GGUFs to the server's blob API, so the local launcher also works when Ollama runs in Docker. Existing destination tags must be protected unless replacement is explicitly requested. Import does not run inference or change context-length settings. Use the model's normal context configuration and validate coding, tool use, and vision behavior before relying on it.

## Validation status

No paid Pod, package installation, GPU processing, or live Ollama import is authorized by writing this automation. Offline verification must cover trial selection, authenticated artifact serving, corruption detection, Pod cleanup, and the Ollama import contract. A small-model live smoke run is still required to validate the complete cloud environment before processing a 27B model.

## References

- [RunPod REST schema](https://rest.runpod.io/v1/openapi.json)
- [RunPod Pod pricing and billing](https://docs.runpod.io/pods/pricing)
- [Heretic 1.4.0 source](https://github.com/p-e-w/heretic/tree/v1.4.0)
- [llama.cpp conversion and quantization](https://github.com/ggml-org/llama.cpp)
- [This checkout's Ollama import API](api.md#create-a-model)

## Commands

Preview without network access or provisioning:

```bash
python3 scripts/runpod_abliterate.py --model OWNER/ORIGINAL-MODEL --name qwen27b-local:q4 --output /absolute/path/new-job
```

Set `RUNPOD_API_KEY` in the launcher environment. Set `HF_TOKEN` only for gated or private source access. Add `--execute` to authorize paid provisioning and remote dependency installation. Use `--quant Q8_0`, `--trials 200`, `--max-hours 24`, or `--gpu` to override defaults. `--ollama-url` selects the destination server, and `OLLAMA_API_KEY` supplies optional server authentication. The destination tag must be new.

Retry a completed download's local import, without any cloud API calls:

```bash
python3 scripts/runpod_abliterate.py --import-only --name qwen27b-local:q4 --output /absolute/path/new-job --execute
```

Reconcile and terminate a job after a launcher interruption, without creating another Pod:

```bash
python3 scripts/runpod_abliterate.py --cleanup-only --name unused --output /absolute/path/new-job --execute
```

If creation returns an ambiguous network failure, cleanup lists Pods and matches the job's unique recorded name. Creation is never automatically retried. Cleanup retries deletion three times and treats an already-absent Pod as terminated. If cleanup cannot reach RunPod, it fails visibly; use the recorded identity in the RunPod console. Partial transfers retry during the active run; once cleanup deletes the worker, missing remote artifacts cannot be recovered. Full processing restart requires a new output directory. Local GGUFs and logs are retained on import failure.

Offline tests:

```bash
python3 -m unittest discover -s scripts -p test_runpod_abliterate.py -v
```
