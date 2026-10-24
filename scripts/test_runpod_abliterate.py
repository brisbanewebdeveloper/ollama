"""Offline lifecycle, transfer, selection, and import regression tests."""

import hashlib
import io
import json
import tempfile
import time
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

from runpod_pipeline import recovery as r
from runpod_pipeline.common import PipelineError, write_private_json
from runpod_pipeline.heretic_driver import Selection


class PipelineTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = {'name': 'unique-job', 'pod_id': 'owned'}
        self.data = b'GGUFtest'
        self.meta = {'size': len(self.data), 'sha256': hashlib.sha256(self.data).hexdigest()}

    @patch.object(r, 'request_json')
    def test_cleanup_owned_pod(self, request):
        r.cleanup(self.state, self.root / 'state.json', 'test')
        self.assertTrue(self.state['terminated'])
        self.assertEqual(request.call_args.kwargs['method'], 'DELETE')

    @patch.object(r.time, 'sleep')
    @patch.object(r, 'request_json')
    def test_cleanup_retries_and_reports_failure(self, request, sleep):
        request.side_effect = OSError('offline')
        with self.assertRaisesRegex(PipelineError, 'Cleanup failed'):
            r.cleanup(self.state, self.root / 'state.json', 'test')
        self.assertEqual(request.call_count, 3)
        self.assertNotIn('terminated', self.state)

    @patch.object(r, 'request_json')
    def test_ambiguous_creation_only_deletes_exact_job(self, request):
        request.side_effect = [[{'id': 'ours', 'name': 'unique-job'},
                                {'id': 'other', 'name': 'someone-else'}], None]
        r.cleanup({'name': 'unique-job'}, self.root / 'state.json', 'test')
        self.assertEqual(request.call_args.args[0], r.API + '/ours')

    @patch.object(r, 'request_json')
    def test_missing_pod_counts_as_cleaned(self, request):
        request.side_effect = PipelineError('HTTP 404 during DELETE; response body omitted')
        r.cleanup(self.state, self.root / 'state.json', 'test')
        self.assertTrue(self.state['terminated'])

    def response(self, data=None, content_range='bytes 0-7/8'):
        result = MagicMock()
        result.__enter__.return_value = result
        result.status = 206
        result.headers = {'Content-Range': content_range}
        result.read.return_value = self.data if data is None else data
        return result

    @patch.object(r, 'open_url')
    def test_download_verifies_and_publishes(self, opened):
        opened.return_value = self.response()
        r.download('https://example.invalid', 'test', {'model.gguf': self.meta}, self.root, time.time()+60)
        self.assertEqual((self.root / 'model.gguf').read_bytes(), self.data)
        self.assertFalse((self.root / 'model.gguf.partial').exists())

    @patch.object(r, 'open_url')
    def test_partial_download_resumes_at_saved_offset(self, opened):
        (self.root / 'model.gguf.partial').write_bytes(self.data[:4])
        opened.return_value = self.response(self.data[4:], 'bytes 4-7/8')
        r.download('https://example.invalid', 'test', {'model.gguf': self.meta}, self.root, time.time()+60)
        self.assertEqual(opened.call_args.kwargs['headers']['Range'], 'bytes=4-7')

    @patch.object(r.time, 'sleep')
    @patch.object(r, 'open_url')
    def test_wrong_range_is_rejected(self, opened, sleep):
        opened.return_value = self.response(content_range='bytes 1-8/8')
        with self.assertRaisesRegex(PipelineError, 'range'):
            r.download('https://example.invalid', 'test', {'model.gguf': self.meta}, self.root, time.time()+60)
        self.assertFalse((self.root / 'model.gguf').exists())

    @patch.object(r, 'open_url')
    def test_corruption_is_not_published(self, opened):
        opened.return_value = self.response(b'badbytes')
        with self.assertRaisesRegex(PipelineError, 'integrity'):
            r.download('https://example.invalid', 'test', {'model.gguf': self.meta}, self.root, time.time()+60)

    def test_download_rejects_traversal_and_expired_deadline(self):
        for name, deadline in [('../evil', time.time()+60), ('model.gguf', 0)]:
            with self.subTest(name=name), self.assertRaises(PipelineError):
                r.download('https://example.invalid', 'test', {name: self.meta}, self.root, deadline)

    @patch.object(r, 'request_json')
    @patch.object(r.http.client, 'HTTPConnection')
    def test_vision_import_includes_text_and_projector(self, connection, request):
        request.side_effect = [{'models': []}, {'status': 'success'}, {'model_info': {'architecture': 'qwen'}}]
        connection.return_value.getresponse.return_value.status = 201
        artifacts = {name: self.meta for name in ['model.gguf', 'mmproj-projector.gguf']}
        for name in artifacts:
            (self.root / name).write_bytes(self.data)
        r.import_model(self.root, artifacts, 'new:tag', 'http://localhost:11434', '')
        create = request.call_args_list[1].kwargs['body']
        self.assertEqual(set(create['files']), set(artifacts))
        self.assertEqual(connection.return_value.send.call_count, 2)

    @patch.object(r, 'request_json')
    def test_existing_tag_is_protected(self, request):
        request.return_value = {'models': [{'name': 'existing:latest'}]}
        with self.assertRaisesRegex(PipelineError, 'exists'):
            r.preflight('http://localhost', 'existing', '')

    def test_private_state_permissions(self):
        path = self.root / 'state.json'
        write_private_json(path, self.state)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_trial_quality_gate_and_unexpected_prompt(self):
        selection = Selection(self.root / 'merged', 0.5, 0.1)
        def candidate(kl):
            return SimpleNamespace(value=SimpleNamespace(user_attrs={'kl_divergence': kl,
                'refusals': 1, 'base_refusals': 90, 'n_bad_prompts': 100}))
        with self.assertRaises(SystemExit):
            selection.select('Which trial do you want to use?', [candidate(0.6)])
        chosen = candidate(0.1)
        self.assertIs(selection.select('Which trial do you want to use?', [chosen]), chosen.value)
        with self.assertRaises(SystemExit):
            selection.select('Upload publicly?', [])


if __name__ == '__main__':
    unittest.main()
