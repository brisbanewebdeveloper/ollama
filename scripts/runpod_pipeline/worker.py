"""Remote model-processing job, launched inside a disposable GPU Pod."""

import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any, Callable

from .common import PipelineError, sha256


def run(command: list[str], root: Path) -> None:
    """Run a stage without a shell or interactive input."""
    subprocess.run(command, cwd=root, stdin=subprocess.DEVNULL, check=True)


def process(config: dict[str, Any], root: Path,
            update: Callable[[str], None]) -> list[Path]:
    root.mkdir(parents=True, exist_ok=True)
    os.environ.update({"HF_HOME": str(root / "hf"), "HF_HUB_DISABLE_TELEMETRY": "1",
                       "DO_NOT_TRACK": "1", "TOKENIZERS_PARALLELISM": "false"})
    update("installing dependencies")
    run(["apt-get", "update"], root)
    run(["apt-get", "install", "-y", "--no-install-recommends",
         "git", "cmake", "build-essential"], root)
    # Install only inside the disposable Pod, never on the local host.
    run([sys.executable, "-m", "venv", "--system-site-packages", str(root / "venv")], root)
    python = str(root / "venv/bin/python")
    run([python, "-m", "pip", "install", "heretic-llm==1.4.0",
         "sentencepiece>=0.1.98,<0.3", "protobuf>=4.21,<5"], root)
    llama = root / "llama.cpp"
    run(["git", "init", str(llama)], root)
    run(["git", "-C", str(llama), "fetch", "--depth", "1",
         "https://github.com/ggml-org/llama.cpp.git", config["llama_commit"]], root)
    run(["git", "-C", str(llama), "checkout", "--detach", "FETCH_HEAD"], root)
    run([python, "-m", "pip", "install", str(llama / "gguf-py")], root)
    run(["cmake", "-S", str(llama), "-B", str(llama / "build"),
         "-DCMAKE_BUILD_TYPE=Release", "-DGGML_CUDA=OFF", "-DLLAMA_CURL=OFF",
         "-DLLAMA_BUILD_TESTS=OFF", "-DLLAMA_BUILD_SERVER=OFF"], root)
    run(["cmake", "--build", str(llama / "build"), "--target", "llama-quantize",
         "--parallel", str(config["cpu_threads"])], root)
    update("downloading original safetensors")
    config.update(snapshot=str(root / "original"), merged=str(root / "merged"))
    job_config = root / "config.json"
    job_config.write_text(json.dumps(config))
    # Run with the venv interpreter so local orchestration needs no HF/ML packages.
    run([python, "-m", "runpod_pipeline.worker", "download", str(job_config)], root)
    update("abliterating and evaluating trials")
    run([python, "-m", "runpod_pipeline.heretic_driver", str(job_config)], root)
    merged = Path(config["merged"])
    if not (merged / "config.json").is_file() or not any(merged.glob("*.safetensors")):
        raise PipelineError("No merged model was exported")
    update("converting and quantizing")
    # Converter and Heretic have different Transformers requirements; isolate them.
    converter = root / "converter"
    run([sys.executable, "-m", "venv", "--system-site-packages", str(converter)], root)
    converter_python = str(converter / "bin/python")
    legacy = llama / "requirements/requirements-convert_legacy_llama.txt"
    run([converter_python, "-m", "pip", "install", "-r", str(legacy),
         str(llama / "gguf-py")], root)
    source = root / "model-bf16.gguf"
    run([converter_python, str(llama / "convert_hf_to_gguf.py"), str(merged),
         "--outfile", str(source), "--outtype", "bf16"], root)
    final = root / f"model-{config['quant']}.gguf"
    run([str(llama / "build/bin/llama-quantize"), str(source), str(final),
         config["quant"], str(config["cpu_threads"])], root)
    files = [final, root / "selection.json"]
    if config["vision"]:
        # Ollama accepts the text GGUF and separate projector in one create request.
        run([converter_python, str(llama / "convert_hf_to_gguf.py"), str(merged),
             "--mmproj", "--outfile", str(root / "projector.gguf"), "--outtype", "f16"], root)
        projectors = list(root.glob("mmproj-*.gguf"))
        if len(projectors) != 1:
            raise PipelineError("Expected one converted vision projector")
        files.extend(projectors)
    for path in files:
        if not path.is_file() or path.stat().st_size == 0:
            raise PipelineError(f"Missing output: {path.name}")
        if path.suffix == ".gguf":
            with path.open("rb") as source_file:
                if source_file.read(4) != b"GGUF":
                    raise PipelineError(f"Invalid GGUF header: {path.name}")
    update("recording provenance")
    provenance = root / "provenance.json"
    with (root / "packages.txt").open("w") as packages:
        subprocess.run([python, "-m", "pip", "freeze"], stdout=packages, check=True)
    provenance.write_text(json.dumps({"job": config,
        "selection": json.loads((root / "selection.json").read_text()),
        "artifacts": {p.name: sha256(p) for p in files},
        "limitations": "Trial metrics do not certify capability retention or absence of backdoors."}, indent=2))
    return files + [provenance, root / "packages.txt"]


def download(config: dict[str, Any]) -> None:
    """Fetch data-only model files at the resolved commit; never remote Python."""
    from huggingface_hub import snapshot_download

    snapshot_download(repo_id=config["model"], revision=config["revision"],
        local_dir=config["snapshot"], allow_patterns=["*.json", "*.safetensors", "*.model",
        "*.txt", "*.tiktoken", "*.jinja", "*.jinja2"])
    root = Path(config["snapshot"])
    model_config = json.loads((root / "config.json").read_text())
    if model_config.get("quantization_config"):
        raise PipelineError("Start from original unquantized safetensors, not a prequantized model")
    if not any(root.glob("*.safetensors")):
        raise PipelineError("The source has no safetensors weights")


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[1] != "download":
        raise SystemExit("This module is an internal worker")
    download(json.loads(Path(sys.argv[2]).read_text()))
