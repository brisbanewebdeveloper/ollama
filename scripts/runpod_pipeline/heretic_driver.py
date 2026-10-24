"""Strict unattended adapter for Heretic 1.4.0's interactive export workflow."""

import json
import math
import sys
from importlib.metadata import version
from pathlib import Path
from typing import Any

from .common import PipelineError


class Selection:
    """Select an improved trial below the configured KL-divergence ceiling."""

    def __init__(self, output: Path, max_kl: float, max_refusal_fraction: float):
        self.output = output
        self.max_kl = max_kl
        self.max_refusal_fraction = max_refusal_fraction
        self.selected = False
        self.save_requested = False

    def select(self, message: str, choices: list[Any]) -> Any:
        if message == "Which trial do you want to use?":
            if self.selected:
                return ""
            candidates = []
            for choice in choices:
                trial = getattr(choice, "value", None)
                attrs = getattr(trial, "user_attrs", {})
                kl = attrs.get("kl_divergence", math.inf)
                refusals = attrs.get("refusals", math.inf)
                total = attrs.get("n_bad_prompts", 0)
                base = attrs.get("base_refusals", 0)
                if (math.isfinite(kl) and 0 <= kl <= self.max_kl and total > 0
                        and 0 <= refusals < base and refusals / total <= self.max_refusal_fraction):
                    candidates.append(trial)
            if not candidates:
                raise SystemExit("No trial met the refusal-improvement and KL-divergence thresholds")
            trial = min(candidates, key=lambda item: (
                item.user_attrs["refusals"], item.user_attrs["kl_divergence"]))
            self.output.parent.mkdir(parents=True, exist_ok=True)
            (self.output.parent / "selection.json").write_text(json.dumps(trial.user_attrs, indent=2))
            self.selected = True
            return trial
        if message == "What do you want to do with the decensored model?" and self.selected:
            if self.save_requested:
                # Upstream catches export errors; never mistake a failed save for success.
                if not (self.output / "config.json").is_file() or not any(self.output.glob("*.safetensors")):
                    raise SystemExit("Heretic did not export merged safetensors successfully")
                return ""
            self.save_requested = True
            return "Save the model to a local folder"
        raise SystemExit(f"Unexpected Heretic prompt; refusing unattended action: {message}")

    def path(self, message: str) -> str:
        if message != "Path to the folder:" or not self.save_requested:
            raise SystemExit("Unexpected Heretic path prompt")
        return str(self.output)


def main() -> None:
    config = json.loads(Path(sys.argv[1]).read_text())
    if version("heretic-llm") != "1.4.0":
        raise PipelineError("The unattended adapter requires Heretic 1.4.0")
    from heretic import main as heretic_main

    selection = Selection(Path(config["merged"]), config["max_kl"], config["max_refusal_fraction"])
    heretic_main.prompt_select = selection.select
    heretic_main.prompt_path = selection.path
    sys.argv = ["heretic", "--model", config["snapshot"],
                "--n-trials", str(config["trials"]),
                "--n-startup-trials", str(min(60, config["trials"])),
                "--batch-size", str(config["batch_size"]),
                "--quantization", config["load_quantization"],
                "--export-strategy", "merge", "--seed", str(config["seed"])]
    heretic_main.run()
    if not selection.save_requested:
        raise PipelineError("Heretic exited without exporting a selected trial")


if __name__ == "__main__":
    main()
