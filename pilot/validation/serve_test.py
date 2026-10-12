"""Lifecycle and probe fences against a tiny fake server; no phebs binary needed."""
import json
import socket
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

import serve

FAKE = textwrap.dedent('''
    import http.server, signal, sys
    mode = sys.argv[2]
    if mode == "die":
        sys.exit(3)
    if mode == "stubborn":
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
    else:
        signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"status":"ok"}')
        def log_message(self, *a): pass
    http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
''')


def free_port() -> int:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.script = self.root / 'fake.py'
        self.script.write_text(FAKE)

    def tearDown(self):
        self.tmp.cleanup()

    def session(self, step, mode):
        port = free_port()
        return serve.ServeSession(self.root / 'run', step, [sys.executable, str(self.script), str(port), mode],
                                  f'http://127.0.0.1:{port}', ready_timeout_s=10, stop_timeout_s=2)

    def test_graceful_phase_records_a_clean_success(self):
        session = self.session('cold-start', 'ok')
        self.assertGreater(session.start(), 0)
        receipt = session.stop('graceful')
        self.assertEqual((receipt['outcome'], receipt['exit_code'], receipt['teardown'], receipt['retained']),
                         ('success', 0, 'clean', []))
        stored = json.loads((self.root / 'run/receipts/cold-start.json').read_text())
        self.assertEqual(serve.validate_serve_receipt(stored), receipt)

    def test_hard_kill_is_a_success_by_sigkill_and_retains_the_log(self):
        session = self.session('hard-death', 'ok')
        session.start()
        receipt = session.stop('hard_kill')
        self.assertEqual((receipt['outcome'], receipt['signal']), ('success', 9))
        self.assertTrue(any(entry.endswith(serve.LOG_NAME) for entry in receipt['retained']))

    def test_an_unplanned_hard_kill_is_a_stop_not_a_success(self):
        session = self.session('aborted', 'ok')
        session.start()
        self.assertEqual(session.stop('hard_kill', reason='probe failed')['outcome'], 'stop')

    def test_ignored_sigterm_is_a_stop_after_the_deadline(self):
        session = self.session('stubborn', 'stubborn')
        session.start()
        receipt = session.stop('graceful')
        self.assertEqual((receipt['outcome'], receipt['signal']), ('stop', 9))
        self.assertIn('exceeded its deadline', receipt['reason'])

    def test_exit_before_ready_fails_and_receipts_are_single_use(self):
        with self.assertRaisesRegex(ValueError, 'before it became ready'):
            self.session('dies', 'die').start()
        receipt = json.loads((self.root / 'run/receipts/dies.json').read_text())
        self.assertEqual((receipt['outcome'], receipt['exit_code']), ('failed', 3))
        with self.assertRaisesRegex(ValueError, 'already exists'):
            self.session('dies', 'ok').start()


class ProbeTests(unittest.TestCase):
    def test_undisclosed_requires_identical_answers_up_to_the_name(self):
        serve.undisclosed((404, b'{"detail":"o/hidden not found"}'), (404, b'{"detail":"o/absent not found"}'), 'o/hidden', 'o/absent')
        with self.assertRaisesRegex(ValueError, 'status'):
            serve.undisclosed((403, b'x'), (404, b'x'), 'a', 'b')
        with self.assertRaisesRegex(ValueError, 'body'):
            serve.undisclosed((404, b'{"rows":[1]}'), (404, b'{"rows":[]}'), 'a', 'b')

    def test_parity_ignores_only_declared_volatile_keys(self):
        volatile = frozenset({'citation', '$schema'})
        http = json.dumps({'$schema': 'u', 'rows': [{'citation': 'x', 'path': 'a.go'}]}).encode()
        serve.parity(http, {'structuredContent': {'rows': [{'citation': 'y', 'path': 'a.go'}]}}, volatile)
        with self.assertRaisesRegex(ValueError, 'differ'):
            serve.parity(http, {'structuredContent': {'rows': [{'path': 'b.go'}]}}, volatile)
        with self.assertRaisesRegex(ValueError, 'error'):
            serve.parity(http, {'isError': True}, volatile)


if __name__ == '__main__':
    unittest.main()
