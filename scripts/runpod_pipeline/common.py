"""Small HTTP and integrity helpers shared by the client and worker."""

import hashlib
import json
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


class PipelineError(RuntimeError):
    """An actionable processing or transport failure."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Do not forward credentials to redirect targets.
        return None


def open_url(url: str, *, method: str = "GET", data: bytes | None = None,
             token: str = "", headers: dict[str, str] | None = None,
             timeout: float = 60):
    request_headers = dict(headers or {})
    if token:
        request_headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, data=data, headers=request_headers, method=method)
    return urllib.request.build_opener(NoRedirect).open(request, timeout=timeout)


def request_json(url: str, *, method: str = "GET", body: Any = None,
                 token: str = "", timeout: float = 60) -> Any:
    data = None if body is None else json.dumps(body).encode()
    try:
        with open_url(url, method=method, data=data, token=token,
                      headers={"Content-Type": "application/json"}, timeout=timeout) as response:
            raw = response.read()
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as error:
        # Provider response bodies can echo submitted credentials.
        raise PipelineError(f"HTTP {error.code} during {method}; response body omitted") from None


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(4 * 1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def write_private_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as target:
        temporary = Path(target.name)
        try:
            json.dump(value, target, indent=2, allow_nan=False)
            target.write("\n")
        except (TypeError, ValueError):
            temporary.unlink()
            raise
    temporary.replace(path)
