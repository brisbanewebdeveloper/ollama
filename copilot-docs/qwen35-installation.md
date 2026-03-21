# Task

Even though I decreased `num_ctx` to 4800 for this model, it still does not work.

Investigate the cause with current codebase and Internet search, and report the solution in the section "Solution To Run".

## Context

- It uses RTX 4090.
- The test model was created with the script `/home/htpt/src/ollama/models/Modelfile.mradermacher.Huihui-Qwen3.5-27B-abliterated-i1-GGUF_IQ4_XS.sh`.
- When I run `/home/htpt/src/ollama/ollama run --verbose Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom "Hello."`, it shows:
    ```
    Error: an error was encountered while running the model: CUDA error: out of memory
    current device: 0, in function ggml_cuda_graph_evaluate_and_capture at //ml/backend/ggml/ggml/src/ggml-cuda/ggml-cuda.cu:4017
    cudaGraphInstantiate(&graph->instance, graph->graph, __null, __null, 0)
    //ml/backend/ggml/ggml/src/ggml-cuda/ggml-cuda.cu:99: CUDA error
    ```

## Solution To Run

### Root cause

`num_ctx=4800` was not actually being used by `ollama run` for this custom model.

- The model creation script still hard-codes `PARAMETER num_ctx 131072`:
    - `/home/htpt/src/ollama/models/Modelfile.mradermacher.Huihui-Qwen3.5-27B-abliterated-i1-GGUF_IQ4_XS.sh`
- The created model confirms that value:
    - `ollama show --modelfile Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom` shows `PARAMETER num_ctx 131072`.
- Runtime also confirms it:
    - `ollama ps` shows `CONTEXT 131072` and ~`22 GB` usage.

From current codebase:

- Option precedence is default -> model -> request (`server/routes.go`, `modelOptions`), so model `num_ctx` overrides default env context.
- Memory scales with context (`llm/server.go`: `KvSize: opts.NumCtx * numParallel`; `fs/ggml/ggml.go`: `GraphSize` multiplies by context/parallel).
- Your error location is CUDA graph instantiate (`ggml-cuda.cu`), which fails when transient graph memory cannot fit.

### Why lowering to 4800 seemed ineffective

- `ollama run` CLI has no `--num_ctx` flag.
- If the model is built with `num_ctx 131072`, plain `ollama run MODEL ...` keeps using that unless you override via API request options or rebuild the model.

### Verified fix

I verified that forcing request options to `num_ctx=4800` works:

```bash
curl -sS http://127.0.0.1:11434/api/generate \
    -H 'Content-Type: application/json' \
    -d '{"model":"Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom","prompt":"Hello.","stream":false,"options":{"num_ctx":4800}}'
```

After this call, `ollama ps` showed `CONTEXT 4800` and lower GPU memory (~`18 GB`), and generation succeeded.

### What to do (recommended)

1. Change your script to set the model context to 4800 (or lower):
     - Replace `echo "PARAMETER num_ctx 131072"` with `echo "PARAMETER num_ctx 4800"`.
2. Recreate the model.
3. Ensure old runner is unloaded before re-test:
     - `ollama stop Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom`
4. Confirm:
     - `ollama show --modelfile ...` includes `PARAMETER num_ctx 4800`
     - `ollama ps` shows `CONTEXT 4800`
5. Re-run:
     - `ollama run --verbose Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom "Hello."`

### If it still crashes at `cudaGraphInstantiate`

Use a CUDA-graph fallback (supported by current ggml code via env check in `common.cuh`):

```bash
GGML_CUDA_DISABLE_GRAPHS=1 ollama run --verbose Huihui-Qwen3.5-27B-abliterated-i1-GGUF:IQ4_XS_custom "Hello."
```

`GGML_CUDA_DISABLE_GRAPHS=1` disables CUDA Graphs in the ggml CUDA backend.
- In code, it checks only whether the env var is present (`getenv(...) != nullptr`), so any set value disables graphs (not just `1`): common.cuh.
- Effect: no CUDA graph capture/instantiate/launch path; it falls back to regular kernel launches (often more stable, sometimes slower).

Also keep Ollama/ggml updated. Similar Huihui abliterated OOM reports are tracked upstream and tied to ggml/CUDA graph behavior:

- https://github.com/ollama/ollama/issues/14044
- https://github.com/ggml-org/llama.cpp/pull/19754

## Solution To Optimize

You can try, but with a single RTX 4090 + Qwen3.5 27B IQ4_XS, `131072` is usually only feasible with heavy compromises.

**Do this first**
- Ensure `131072` is actually applied (your earlier run used model default, not request override).
- Use API options (since `ollama run` has no `--num_ctx`):
  - `options.num_ctx: 131072`
  - `options.num_batch: 32` or `64` (much lower than default `512`)
  - `options.num_gpu`: lower than full offload (e.g. start around `16-24`, tune)

**Reduce VRAM pressure**
- Set `OLLAMA_NUM_PARALLEL=1`
- Set `OLLAMA_FLASH_ATTENTION=1`
- Set `OLLAMA_KV_CACHE_TYPE=q4_0` (or `q8_0` if needed)
- Set `GGML_CUDA_DISABLE_GRAPHS=1` (your OOM is at `cudaGraphInstantiate`)
- Optionally set `OLLAMA_GPU_OVERHEAD=2147483648` (2 GiB) to force more headroom

Default of `OLLAMA_GPU_OVERHEAD` is `0` (bytes).
- Defined as `Uint64("OLLAMA_GPU_OVERHEAD", 0)`: config.go.

**Important reality**
- If it still OOMs, that’s a hardware limit for this model/context combo on one 24 GB GPU.
- Then only practical options are: more GPUs / larger VRAM, smaller model, or lower context.

If you want, I can give you an exact `curl` payload and a tuned Modelfile for `131072` that prioritizes “works reliably” over speed.
