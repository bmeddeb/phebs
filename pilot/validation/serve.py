"""Real-binary serve lifecycle, authorization and transport-parity drivers (T48.3).

``ServeSession`` runs one long-lived server as a forked session leader with its
log in the step workspace, waits for readiness, and ends it either gracefully
(SIGTERM, bounded wait, then group kill) or as a hard death (immediate group
SIGKILL). Every phase writes one write-once ``harness-serve-receipt-v1`` with
readiness latency, exit/signal, ``wait4`` peak RSS (the server plus children
it reaped), closed host facts and the group-drain teardown class; a failed or
hard-killed phase retains its bounded workspace. The probe helpers make two
pack-independent operating claims checkable: a denied repository is
indistinguishable from an absent one, and HTTP and MCP return the same exact
result once request-bound tokens are removed. Supervision covers the server's
own session only, as in ``receipts``.
"""
from __future__ import annotations

import json
import os
import signal
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

import receipts
from corpus import canonical_bytes

SERVE_RECEIPT_SCHEMA = 'harness-serve-receipt-v1'
STOP_MODES = ('graceful', 'hard_kill')
MAX_RESPONSE = 32 << 20
LOG_NAME = 'serve.log'


class ServeError(ValueError):
    """A lifecycle phase or probe violated its contract."""


def http_get(base: str, path: str, params: dict | None = None, token: str | None = None,
             timeout: float = 60) -> tuple[int, bytes]:
    url = base + path + ('?' + urllib.parse.urlencode(params) if params else '')
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=timeout) as r:
            body = r.read(MAX_RESPONSE + 1)
            status = r.status
    except urllib.error.HTTPError as error:
        body, status = error.read(MAX_RESPONSE + 1), error.code
    if len(body) > MAX_RESPONSE:
        raise ServeError('response exceeds bound')
    return status, body


def mcp_call(base: str, tool: str, arguments: dict, token: str | None = None, timeout: float = 60) -> dict:
    """One stateless streamable-HTTP tools/call; returns the JSON-RPC result object."""
    payload = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                          'params': {'name': tool, 'arguments': arguments}}).encode()
    headers = {'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    with urllib.request.urlopen(urllib.request.Request(base + '/api/mcp', data=payload, headers=headers),
                                timeout=timeout) as r:
        raw = r.read(MAX_RESPONSE + 1)
        kind = r.headers.get('Content-Type', '')
    if len(raw) > MAX_RESPONSE:
        raise ServeError('MCP response exceeds bound')
    if kind.startswith('text/event-stream'):
        data = [line[len(b'data: '):] for line in raw.splitlines() if line.startswith(b'data: ')]
        if len(data) != 1:
            raise ServeError('MCP stream did not carry exactly one message')
        raw = data[0]
    message = json.loads(raw)
    if message.get('id') != 1 or 'result' not in message:
        raise ServeError(f"MCP call failed: {message.get('error')}")
    return message['result']


def strip_volatile(value, volatile: frozenset[str]):
    """Drop request-bound keys (citation tokens, cursors, schema URLs) at any depth."""
    if isinstance(value, dict):
        return {k: strip_volatile(v, volatile) for k, v in value.items() if k not in volatile}
    if isinstance(value, list):
        return [strip_volatile(v, volatile) for v in value]
    return value


def parity(http_body: bytes, mcp_result: dict, volatile: frozenset[str]) -> None:
    """HTTP and MCP must agree exactly on the transport-neutral result."""
    if mcp_result.get('isError'):
        raise ServeError('MCP returned an error where HTTP returned a result')
    left = strip_volatile(json.loads(http_body), volatile)
    right = strip_volatile(mcp_result.get('structuredContent'), volatile)
    if left != right:
        raise ServeError('HTTP and MCP results differ')


def undisclosed(denied: tuple[int, bytes], absent: tuple[int, bytes], denied_name: str, absent_name: str) -> None:
    """A denied repository must answer exactly like an absent one, names aside."""
    if denied[0] != absent[0]:
        raise ServeError(f'denied status {denied[0]} differs from absent status {absent[0]}')
    if denied[1].replace(denied_name.encode(), b'<repository>') != absent[1].replace(absent_name.encode(), b'<repository>'):
        raise ServeError('denied response body differs from the absent response body')


class ServeSession:
    """One server phase: start, wait ready, then stop gracefully or hard-kill."""

    def __init__(self, run_root, step: str, argv: list[str], base: str, *, env: dict | None = None,
                 ready_timeout_s: float = 120, stop_timeout_s: float = 30, cwd=None):
        self.root = Path(run_root)
        self.step = receipts._step_name(step)
        if not argv or any(not isinstance(a, str) or not a for a in argv):
            raise ServeError('argv must be non-empty strings')
        self.argv, self.base, self.env, self.cwd = list(argv), base, env, cwd
        self.ready_timeout_s, self.stop_timeout_s = ready_timeout_s, stop_timeout_s
        self.pid = None
        self.ready_seconds = None

    def start(self) -> float:
        receipts.ensure_run_layout(self.root)
        if (self.root / receipts.RECEIPTS_DIR / f'{self.step}.json').exists():
            raise ServeError(f'a receipt for {self.step!r} already exists')
        self.workspace = self.root / receipts.SCRATCH_DIR / self.step
        self.workspace.mkdir(parents=True)
        self.started = receipts.utc_now()
        self.clock = time.monotonic()
        log = os.open(self.workspace / LOG_NAME, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        pid = os.fork()
        if pid == 0:  # pragma: no cover - the child either execs or exits
            try:
                os.setsid()
                os.dup2(log, 1)
                os.dup2(log, 2)
                os.chdir(self.cwd or self.workspace)
                os.execvpe(self.argv[0], self.argv, self.env if self.env is not None else os.environ)
            except BaseException:
                os._exit(127)
        os.close(log)
        self.pid = pid
        while True:
            done, status, rusage = os.wait4(pid, os.WNOHANG)
            if done == pid:
                self._finish('failed', status, rusage, mode=None, reason='exited before ready')
                raise ServeError('server exited before it became ready')
            try:
                if http_get(self.base, '/api/health', timeout=2)[0] == 200:
                    self.ready_seconds = round(time.monotonic() - self.clock, 3)
                    return self.ready_seconds
            except OSError:
                pass
            if time.monotonic() - self.clock > self.ready_timeout_s:
                self.stop('hard_kill', reason='not ready before deadline')
                raise ServeError('server did not become ready before its deadline')
            time.sleep(0.25)

    def stop(self, mode: str, reason: str = '') -> dict:
        if mode not in STOP_MODES or self.pid is None:
            raise ServeError('stop needs a started session and a known mode')
        if mode == 'graceful':
            os.kill(self.pid, signal.SIGTERM)
            limit = time.monotonic() + self.stop_timeout_s
            while time.monotonic() <= limit:
                done, status, rusage = os.wait4(self.pid, os.WNOHANG)
                if done == self.pid:
                    return self._finish(None, status, rusage, mode, reason)
                time.sleep(receipts.POLL_SECONDS)
            reason = (reason + '; ' if reason else '') + 'graceful stop exceeded its deadline'
        os.killpg(self.pid, signal.SIGKILL)
        limit = time.monotonic() + receipts.REAP_SECONDS
        while time.monotonic() <= limit:
            done, status, rusage = os.wait4(self.pid, os.WNOHANG)
            if done == self.pid:
                return self._finish(None, status, rusage, mode, reason, killed=True)
            time.sleep(receipts.POLL_SECONDS)
        return self._finish('stop', None, None, mode, reason + '; not reaped', killed=True)

    def _finish(self, outcome, status, rusage, mode, reason, killed=False) -> dict:
        wall = round(time.monotonic() - self.clock, 3)
        exit_code = signal_number = None
        if status is not None and os.WIFEXITED(status):
            exit_code = os.WEXITSTATUS(status)
        elif status is not None and os.WIFSIGNALED(status):
            signal_number = os.WTERMSIG(status)
        if outcome is None:
            graceful_clean = mode == 'graceful' and not killed and exit_code == 0
            # A hard kill succeeds only as the planned end of a phase, never as a forced abort.
            planned_kill = mode == 'hard_kill' and signal_number == signal.SIGKILL and not reason
            outcome = 'success' if graceful_clean or planned_kill else 'stop'
        teardown = receipts._drain_group(self.pid, killed)
        retained = []
        if outcome != 'success' or mode == 'hard_kill':
            retained = receipts._retain_workspace(self.workspace, self.root / receipts.RETAINED_DIR / self.step, self.root)
        log_path = self.workspace / LOG_NAME
        log_digest = receipts.sha256_file(log_path) if log_path.is_file() else None
        import shutil
        shutil.rmtree(self.workspace)
        receipt = {'schema': SERVE_RECEIPT_SCHEMA, 'step': self.step, 'argv': self.argv, 'stop_mode': mode,
                   'reason': reason, 'outcome': outcome, 'ready_seconds': self.ready_seconds,
                   'exit_code': exit_code, 'signal': signal_number, 'started_utc': self.started,
                   'finished_utc': receipts.utc_now(), 'wall_seconds': wall,
                   'max_rss_kib': receipts._max_rss_kib(rusage) if rusage is not None else 0,
                   'host': receipts.host_facts(), 'teardown': teardown, 'log_sha256': log_digest,
                   'retained': retained}
        validate_serve_receipt(receipt)
        path = self.root / receipts.RECEIPTS_DIR / f'{self.step}.json'
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        try:
            os.write(fd, canonical_bytes(receipt))
            os.fsync(fd)
        finally:
            os.close(fd)
        receipts.fsync_dir(path.parent)
        self.pid = None
        return receipt


def validate_serve_receipt(value: object) -> dict:
    fields = {'schema', 'step', 'argv', 'stop_mode', 'reason', 'outcome', 'ready_seconds', 'exit_code', 'signal',
              'started_utc', 'finished_utc', 'wall_seconds', 'max_rss_kib', 'host', 'teardown', 'log_sha256',
              'retained'}
    if not isinstance(value, dict) or set(value) != fields or value['schema'] != SERVE_RECEIPT_SCHEMA:
        raise ServeError('serve receipt fields are missing, unknown or of another schema')
    if value['outcome'] not in receipts.OUTCOMES or value['teardown'] not in receipts.TEARDOWNS:
        raise ServeError('serve receipt outcome or teardown is unknown')
    if value['stop_mode'] not in STOP_MODES + (None,):
        raise ServeError('serve receipt stop mode is unknown')
    if value['outcome'] == 'success':
        if value['ready_seconds'] is None or value['teardown'] == 'unclean':
            raise ServeError('a successful phase was ready and drained')
        if value['stop_mode'] == 'graceful' and (value['exit_code'] != 0 or value['signal'] is not None):
            raise ServeError('a successful graceful phase exits zero')
        if value['stop_mode'] == 'hard_kill' and value['signal'] != signal.SIGKILL:
            raise ServeError('a successful hard-kill phase ends by SIGKILL')
    return dict(value)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def oidc_login(base: str, key_name: str, timeout: float = 30) -> tuple[dict, str]:
    """Complete one browser-style OIDC login and mint that user's own API key.

    Redirects are followed by hand so each hop is checked: phebs -> issuer
    authorize -> phebs callback -> session. Returns (status user, bearer token).
    """
    import http.cookiejar
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), _NoRedirect)

    def hop(url: str, expect: int) -> str:
        try:
            opener.open(url, timeout=timeout)
            raise ServeError(f'{url} did not redirect')
        except urllib.error.HTTPError as error:
            if error.code != expect:
                raise ServeError(f'{urllib.parse.urlsplit(url).path} returned {error.code}, not {expect}') from None
            return urllib.parse.urljoin(url, error.headers['Location'])

    authorize = hop(base + '/api/auth/oidc/start', 302)
    callback = hop(authorize, 302)
    if not callback.startswith(base + '/api/auth/oidc/callback'):
        raise ServeError('issuer redirected outside the configured callback')
    hop(callback, 303)
    with opener.open(base + '/api/auth/status', timeout=timeout) as r:
        status = json.loads(r.read(MAX_RESPONSE))
    if not status.get('authenticated') or not status.get('csrf_token'):
        raise ServeError('OIDC login did not establish a session')
    request = urllib.request.Request(base + '/api/auth/keys', data=json.dumps({'name': key_name}).encode(),
                                     headers={'Content-Type': 'application/json', 'X-CSRF-Token': status['csrf_token']})
    with opener.open(request, timeout=timeout) as r:
        token = json.loads(r.read(MAX_RESPONSE))['token']
    return status['user'], token
