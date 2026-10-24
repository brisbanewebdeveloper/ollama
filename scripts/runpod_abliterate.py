"""Provision a disposable RunPod worker and download its GGUF into Ollama."""

import argparse
import base64
import io
import json
import os
import secrets
import tarfile
import time
from pathlib import Path

from runpod_pipeline.common import PipelineError, request_json, write_private_json
from runpod_pipeline.recovery import cleanup, download, import_model, preflight


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--model', help='Original Hugging Face repository ID')
    parser.add_argument('--name', required=True, help='New local Ollama tag')
    parser.add_argument('--revision', default='main')
    parser.add_argument('--quant', choices=['Q4_K_M', 'Q5_K_M', 'Q6_K', 'Q8_0'], default='Q4_K_M')
    parser.add_argument('--gpu', default='NVIDIA A100 80GB PCIe')
    parser.add_argument('--trials', type=int, default=200)
    parser.add_argument('--max-hours', type=float, default=24)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--execute', action='store_true', help='Authorize paid provisioning and remote dependency installation')
    parser.add_argument('--import-only', action='store_true', help='Import verified saved artifacts; no cloud calls')
    parser.add_argument('--cleanup-only', action='store_true', help='Terminate the Pod recorded in output/state.json')
    parser.add_argument('--ollama-url', default=os.environ.get('OLLAMA_HOST', 'http://127.0.0.1:11434'))
    args = parser.parse_args()
    args.ollama_url = args.ollama_url.rstrip('/')
    if args.import_only and args.cleanup_only:
        parser.error('Choose one recovery mode')
    if not (args.import_only or args.cleanup_only) and not args.model:
        parser.error('--model is required for processing')
    if args.trials < 1 or args.max_hours <= 0:
        parser.error('Trials and runtime must be positive')
    if not args.execute:
        print(json.dumps(vars(args), default=str, indent=2))
        print('Preview only. Add --execute to authorize paid provisioning.')
        return
    state_path = args.output / 'state.json'
    ollama_token = os.environ.get('OLLAMA_API_KEY', '')
    if args.import_only:
        state = json.loads(state_path.read_text())
        if state.get('remote_state') != 'ready':
            raise PipelineError('No completed remote job to import')
        import_model(args.output, state['artifacts'], args.name, args.ollama_url, ollama_token)
        return
    key = os.environ['RUNPOD_API_KEY']
    if args.cleanup_only:
        cleanup(json.loads(state_path.read_text()), state_path, key)
        return
    import re
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', args.model):
        parser.error('Expected a Hugging Face owner/repository ID')
    hf = os.environ.get('HF_TOKEN', '')
    preflight(args.ollama_url, args.name, ollama_token)
    info = request_json(f'https://huggingface.co/api/models/{args.model}/revision/{args.revision}', token=hf)
    revision = info['sha']
    config = request_json(f'https://huggingface.co/{args.model}/raw/{revision}/config.json', token=hf)
    if config.get('auto_map') or config.get('quantization_config'):
        raise PipelineError('Use original safetensors with a native Transformers architecture')
    args.output.mkdir(parents=True, exist_ok=False, mode=0o700)
    job = dict(model=args.model, revision=revision, quant=args.quant, trials=args.trials,
               max_kl=0.5, max_refusal_fraction=0.1, seed=42, batch_size=1,
               load_quantization='none', cpu_threads=8, vision=bool(config.get('vision_config')),
               llama_commit='edc353842c1be9b4ebc27460a1da4526a026d3a9')
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
        for path in Path(__file__).with_name('runpod_pipeline').glob('*.py'):
            archive.add(path, arcname='runpod_pipeline/' + path.name)
    token = secrets.token_urlsafe(32)
    env = dict(PIPELINE_BUNDLE=base64.b64encode(buffer.getvalue()).decode(),
               PIPELINE_CONFIG=json.dumps(job), ARTIFACT_TOKEN=token)
    if hf:
        env['HF_TOKEN'] = hf
    bootstrap = "import os,io,tarfile,base64;os.makedirs('/opt/pipeline',exist_ok=True);tarfile.open(fileobj=io.BytesIO(base64.b64decode(os.environ.pop('PIPELINE_BUNDLE'))),mode='r:gz').extractall('/opt/pipeline');os.environ['PYTHONPATH']='/opt/pipeline';os.execvp('python',['python','-m','runpod_pipeline.service'])"
    payload = dict(name='abliterate-' + secrets.token_hex(8),
        imageName='pytorch/pytorch:2.8.0-cuda12.8-cudnn9-devel', cloudType='SECURE',
        gpuTypeIds=[args.gpu], gpuCount=1, interruptible=False,
        containerDiskInGb=300, volumeInGb=0, minRAMPerGPU=96, minVCPUPerGPU=8,
        ports=['8080/http'], dockerEntrypoint=['python'], dockerStartCmd=['-u', '-c', bootstrap], env=env)
    state = {'name': payload['name'], 'deadline': time.time() + args.max_hours * 3600}
    state_path = args.output / 'state.json'
    write_private_json(state_path, state)
    try:
        pod = request_json('https://rest.runpod.io/v1/pods', method='POST', body=payload, token=key)
        state['pod_id'] = pod['id']
        write_private_json(state_path, state)
        url = f"https://{pod['id']}-8080.proxy.runpod.net"
        print(f"Allocated Pod {pod['id']}; keep this launcher running for cleanup", flush=True)
        while time.time() < state['deadline']:
            try:
                status = request_json(url + '/status', token=token, timeout=30)
            except (OSError, PipelineError):
                time.sleep(10)
                continue
            print(status.get('stage', status['state']), flush=True)
            if status['state'] in {'ready', 'failed'}:
                break
            time.sleep(20)
        else:
            raise PipelineError('Runtime limit exceeded')
        state['artifacts'] = status['artifacts']
        state['remote_state'] = status['state']
        write_private_json(state_path, state)
        download(url, token, status['artifacts'], args.output, state['deadline'])
        if status['state'] != 'ready':
            raise PipelineError('Remote processing failed; inspect downloaded job.log')
    finally:
        cleanup(state, state_path, key)
    import_model(args.output, state['artifacts'], args.name, args.ollama_url, ollama_token)


if __name__ == '__main__':
    main()
