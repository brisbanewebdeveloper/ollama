"""Authenticated status and bounded artifact reads through RunPod's HTTPS proxy."""

import hmac
import json
import os
import re
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

from .common import sha256

CHUNK = 8 * 1024 * 1024


class Job:
    def __init__(self, root: Path, config: dict[str, Any]):
        self.root = root
        self.config = config
        self.lock = threading.Lock()
        self.status: dict[str, Any] = {"state": "running", "stage": "starting", "artifacts": {}}

    def update(self, stage: str) -> None:
        with self.lock:
            self.status["stage"] = stage

    def execute(self) -> None:
        try:
            self._execute()
        except Exception as error:
            # Preserve a terminal status even if the supervisor itself fails.
            log = self.root / 'supervisor-error.txt'
            log.write_text(type(error).__name__ + ': remote supervisor failed; inspect Pod logs')
            with self.lock:
                self.status = {'state': 'failed', 'stage': 'supervisor failed',
                    'artifacts': {log.name: {'size': log.stat().st_size, 'sha256': sha256(log)}}}

    def _execute(self) -> None:
        config_path = self.root / "job-input.json"
        config_path.write_text(json.dumps(self.config))
        log = self.root / "job.log"
        # Keep subprocess output out of HTTP responses and local terminal logs.
        with log.open("wb") as output:
            result = subprocess.run([sys.executable, "-m", "runpod_pipeline.service",
                "process", str(config_path)], stdout=output, stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL, cwd=self.root)
        paths = [log]
        if result.returncode == 0:
            names = json.loads((self.root / "outputs.json").read_text())
            paths.extend(self.root / name for name in names)
        artifacts = {path.name: {"size": path.stat().st_size, "sha256": sha256(path)} for path in paths}
        with self.lock:
            self.status = {"state": "ready" if result.returncode == 0 else "failed",
                           "stage": "finished", "artifacts": artifacts,
                           "exit_code": result.returncode}

    def snapshot(self) -> dict[str, Any]:
        with self.lock:
            result = dict(self.status)
        stage_file = self.root / "stage.json"
        if result["state"] == "running" and stage_file.exists():
            try:
                result["stage"] = json.loads(stage_file.read_text())
            except (OSError, ValueError):
                pass  # A concurrent atomic update will be available on the next poll.
        return result


def make_handler(job: Job, token: str) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format: str, *args: Any) -> None:
            pass

        def do_GET(self) -> None:
            if not hmac.compare_digest(self.headers.get("Authorization", ""), f"Bearer {token}"):
                self.send_error(401)
                return
            status = job.snapshot()
            if self.path == "/status":
                data = json.dumps(status).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
                return
            name = self.path.removeprefix("/artifacts/")
            if not self.path.startswith("/artifacts/") or name not in status["artifacts"]:
                self.send_error(404)
                return
            match = re.fullmatch(r"bytes=(\d+)-(\d+)", self.headers.get("Range", ""))
            size = status["artifacts"][name]["size"]
            if not match:
                self.send_error(416)
                return
            start, end = map(int, match.groups())
            if start > end or start >= size or end >= size or end - start + 1 > CHUNK:
                self.send_error(416)
                return
            with (job.root / name).open("rb") as source:
                source.seek(start)
                data = source.read(end - start + 1)
            self.send_response(206)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(data)))
            self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
            self.end_headers()
            self.wfile.write(data)

    return Handler


def main() -> None:
    os.umask(0o077)
    root = Path("/workspace/job")
    root.mkdir(parents=True, exist_ok=True)
    if len(sys.argv) > 1 and sys.argv[1] == "process":
        from .worker import process

        config = json.loads(Path(sys.argv[2]).read_text())

        def update(stage: str) -> None:
            temporary = root / "stage.tmp"
            temporary.write_text(json.dumps(stage))
            temporary.replace(root / "stage.json")

        outputs = process(config, root, update)
        (root / "outputs.json").write_text(json.dumps([p.name for p in outputs]))
        return
    job = Job(root, json.loads(os.environ.pop("PIPELINE_CONFIG")))
    token = os.environ.pop("ARTIFACT_TOKEN")
    server = ThreadingHTTPServer(("0.0.0.0", 8080), make_handler(job, token))
    threading.Thread(target=job.execute, daemon=True).start()
    server.serve_forever()


if __name__ == "__main__":
    main()
