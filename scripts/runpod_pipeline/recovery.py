"""Recover owned Pods, transfer artifacts, and import multi-file models."""

import http.client
import re
import time
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from .common import PipelineError, open_url, request_json, sha256, write_private_json

API = 'https://rest.runpod.io/v1/pods'
CHUNK = 8 * 1024 * 1024


def cleanup(state: dict[str, Any], path: Path, key: str) -> None:
    """Reconcile ambiguous creation by unique job name; never retry creation."""
    if state.get('terminated'):
        return
    if state.get('pod_id'):
        identifiers = [state['pod_id']]
    else:
        pods = request_json(API, token=key)
        identifiers = [pod['id'] for pod in pods if pod.get('name') == state['name']]
    for identifier in identifiers:
        for attempt in range(3):
            try:
                request_json(API + '/' + identifier, method='DELETE', token=key)
                break
            except (OSError, PipelineError) as error:
                if isinstance(error, PipelineError) and 'HTTP 404 ' in str(error):
                    break
                if attempt == 2:
                    raise PipelineError(f'Cleanup failed for Pod {identifier}; terminate it in RunPod') from error
                time.sleep(2)
    state['terminated'] = True
    write_private_json(path, state)


def download(url: str, token: str, artifacts: dict[str, Any], directory: Path, deadline: float) -> None:
    """Retry short range reads and retain partial files until SHA256 verifies."""
    for name, metadata in artifacts.items():
        if not re.fullmatch(r'[A-Za-z0-9_.-]+', name) or name in {'.', '..'}:
            raise PipelineError('Invalid artifact name')
        size, digest = metadata['size'], metadata['sha256']
        if not isinstance(size, int) or size < 0 or not re.fullmatch('[0-9a-f]{64}', digest):
            raise PipelineError('Invalid artifact metadata')
        target = directory / name
        if target.exists():
            if target.stat().st_size == size and sha256(target) == digest:
                continue
            raise PipelineError('Existing artifact differs: ' + name)
        partial = directory / (name + '.partial')
        offset = partial.stat().st_size if partial.exists() else 0
        if offset > size:
            raise PipelineError('Oversized partial artifact')
        with partial.open('ab') as stream:
            while offset < size:
                end = min(offset + CHUNK, size) - 1
                for attempt in range(3):
                    remaining = deadline - time.time()
                    if remaining <= 0:
                        raise PipelineError('Download runtime limit exceeded')
                    try:
                        with open_url(url + '/artifacts/' + name, token=token,
                                headers={'Range': f'bytes={offset}-{end}'},
                                timeout=min(60, remaining)) as response:
                            data = response.read(CHUNK + 1)
                            if (response.status != 206 or len(data) != end - offset + 1 or
                                    response.headers.get('Content-Range') != f'bytes {offset}-{end}/{size}'):
                                raise PipelineError('Invalid download range response')
                        break
                    except (OSError, PipelineError):
                        if attempt == 2:
                            raise
                        time.sleep(min(2, max(0, deadline - time.time())))
                stream.write(data)
                stream.flush()
                offset += len(data)
        if sha256(partial) != digest:
            raise PipelineError('Artifact integrity check failed: ' + name)
        partial.replace(target)


def preflight(url: str, name: str, token: str) -> None:
    tags = request_json(url + '/api/tags', token=token)
    normalized = name if ':' in name.rsplit('/', 1)[-1] else name + ':latest'
    if any(model['name'] == normalized for model in tags.get('models', [])):
        raise PipelineError('Destination tag exists; choose a new name')


def import_model(directory: Path, artifacts: dict[str, Any], name: str, url: str, token: str) -> None:
    """Upload text and projector GGUF blobs without requiring shared Docker paths."""
    preflight(url, name, token)
    parsed = urlsplit(url)
    if parsed.scheme not in {'http', 'https'} or not parsed.hostname or parsed.username:
        raise PipelineError('Invalid Ollama URL')
    files = {}
    for filename, metadata in artifacts.items():
        if not filename.endswith('.gguf'):
            continue
        if Path(filename).name != filename:
            raise PipelineError('Invalid GGUF filename')
        path = directory / filename
        if path.stat().st_size != metadata['size'] or sha256(path) != metadata['sha256']:
            raise PipelineError('Local GGUF integrity check failed')
        digest = 'sha256:' + metadata['sha256']
        connection_type = http.client.HTTPSConnection if parsed.scheme == 'https' else http.client.HTTPConnection
        connection = connection_type(parsed.hostname, parsed.port, timeout=120)
        try:
            connection.putrequest('POST', parsed.path.rstrip('/') + '/api/blobs/' + digest)
            connection.putheader('Content-Length', str(path.stat().st_size))
            connection.putheader('Content-Type', 'application/octet-stream')
            if token:
                connection.putheader('Authorization', 'Bearer ' + token)
            connection.endheaders()
            with path.open('rb') as source:
                for block in iter(lambda: source.read(CHUNK), b''):
                    connection.send(block)
            response = connection.getresponse()
            response.read()
            if response.status != 201:
                raise PipelineError(f'Ollama upload failed: HTTP {response.status}')
        finally:
            connection.close()
        files[filename] = digest
    if not files:
        raise PipelineError('No GGUF artifacts')
    result = request_json(url + '/api/create', method='POST', token=token,
        body={'model': name, 'files': files, 'stream': False}, timeout=3600)
    if result.get('error') or result.get('status') != 'success':
        raise PipelineError('Ollama model creation failed')
    shown = request_json(url + '/api/show', method='POST', token=token, body={'model': name})
    if not shown.get('model_info'):
        raise PipelineError('Ollama model metadata missing after import')
