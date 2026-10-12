"""Loopback-only test OIDC issuer for real-binary authorization rehearsals (T48.3).

Serves discovery, JWKS, an authorization endpoint that immediately redirects
with a one-time code, and a token endpoint that checks the client secret,
redirect URI and PKCE S256 verifier before issuing an RS256 ID token. It
issues the configured identities in order, one per login, so a rehearsal can
deterministically create the first (administrator) and a later (ordinary)
account. It binds 127.0.0.1 only and must never front a real deployment.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import http.server
import json
import secrets
import signal
import sys
import threading
import time
import urllib.parse

from cryptography.hazmat.primitives import hashes, serialization  # noqa: F401
from cryptography.hazmat.primitives.asymmetric import padding, rsa


def b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b'=').decode()


class Issuer:
    def __init__(self, base: str, client_id: str, client_secret: str, identities: list[dict]):
        self.base, self.client_id, self.client_secret = base, client_id, client_secret
        self.identities = list(identities)
        self.key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        self.kid = secrets.token_hex(8)
        self.codes: dict[str, dict] = {}
        self.lock = threading.Lock()

    def jwks(self) -> dict:
        numbers = self.key.public_key().public_numbers()
        to_bytes = lambda n: n.to_bytes((n.bit_length() + 7) // 8, 'big')  # noqa: E731
        return {'keys': [{'kty': 'RSA', 'use': 'sig', 'alg': 'RS256', 'kid': self.kid,
                          'n': b64url(to_bytes(numbers.n)), 'e': b64url(to_bytes(numbers.e))}]}

    def discovery(self) -> dict:
        return {'issuer': self.base, 'authorization_endpoint': self.base + '/authorize',
                'token_endpoint': self.base + '/token', 'jwks_uri': self.base + '/jwks',
                'response_types_supported': ['code'], 'subject_types_supported': ['public'],
                'id_token_signing_alg_values_supported': ['RS256'],
                'code_challenge_methods_supported': ['S256'],
                'token_endpoint_auth_methods_supported': ['client_secret_basic', 'client_secret_post']}

    def authorize(self, query: dict) -> str:
        if query.get('client_id') != self.client_id or query.get('response_type') != 'code' \
                or query.get('code_challenge_method') != 'S256' or not query.get('code_challenge'):
            raise ValueError('invalid authorization request')
        with self.lock:
            if not self.identities:
                raise ValueError('no identity left to issue')
            identity = self.identities.pop(0)
            code = secrets.token_urlsafe(24)
            self.codes[code] = {'identity': identity, 'nonce': query.get('nonce', ''),
                                'challenge': query['code_challenge'], 'redirect_uri': query['redirect_uri']}
        return query['redirect_uri'] + '?' + urllib.parse.urlencode({'code': code, 'state': query.get('state', '')})

    def token(self, form: dict, basic: tuple[str, str] | None) -> dict:
        client = basic or (form.get('client_id'), form.get('client_secret'))
        if client != (self.client_id, self.client_secret) or form.get('grant_type') != 'authorization_code':
            raise ValueError('invalid client or grant')
        with self.lock:
            grant = self.codes.pop(form.get('code', ''), None)
        if grant is None or form.get('redirect_uri') != grant['redirect_uri']:
            raise ValueError('unknown, reused or misdirected code')
        if b64url(hashlib.sha256(form.get('code_verifier', '').encode()).digest()) != grant['challenge']:
            raise ValueError('PKCE verifier mismatch')
        now = int(time.time())
        claims = {'iss': self.base, 'aud': self.client_id, 'iat': now, 'exp': now + 300,
                  'nonce': grant['nonce'], 'email_verified': True, **grant['identity']}
        header = {'alg': 'RS256', 'typ': 'JWT', 'kid': self.kid}
        signing = (b64url(json.dumps(header).encode()) + '.' + b64url(json.dumps(claims).encode())).encode()
        signature = self.key.sign(signing, padding.PKCS1v15(), hashes.SHA256())
        return {'access_token': secrets.token_urlsafe(24), 'token_type': 'Bearer', 'expires_in': 300,
                'id_token': signing.decode() + '.' + b64url(signature)}


def handler_for(issuer: Issuer):
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, status: int, body: dict | None = None, location: str | None = None):
            self.send_response(status)
            if location:
                self.send_header('Location', location)
            payload = json.dumps(body or {}).encode()
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self):
            url = urllib.parse.urlsplit(self.path)
            try:
                if url.path in ('/api/health', '/health'):
                    self.reply(200, {'status': 'ok'})
                elif url.path == '/.well-known/openid-configuration':
                    self.reply(200, issuer.discovery())
                elif url.path == '/jwks':
                    self.reply(200, issuer.jwks())
                elif url.path == '/authorize':
                    self.reply(302, location=issuer.authorize(dict(urllib.parse.parse_qsl(url.query))))
                else:
                    self.reply(404, {'error': 'not_found'})
            except ValueError as error:
                self.reply(400, {'error': 'invalid_request', 'error_description': str(error)})

        def do_POST(self):
            if urllib.parse.urlsplit(self.path).path != '/token':
                return self.reply(404, {'error': 'not_found'})
            length = int(self.headers.get('Content-Length', '0'))
            form = dict(urllib.parse.parse_qsl(self.rfile.read(min(length, 65536)).decode()))
            basic = None
            auth = self.headers.get('Authorization', '')
            if auth.startswith('Basic '):
                user, _, password = base64.b64decode(auth[6:]).decode().partition(':')
                basic = (urllib.parse.unquote_plus(user), urllib.parse.unquote_plus(password))
            try:
                self.reply(200, issuer.token(form, basic))
            except ValueError as error:
                self.reply(400, {'error': 'invalid_grant', 'error_description': str(error)})
    return Handler


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--client-id', required=True)
    parser.add_argument('--client-secret-file', required=True)
    parser.add_argument('--identities', required=True, help='JSON list of {sub, email, name}')
    args = parser.parse_args()
    secret = open(args.client_secret_file).read().strip()
    issuer = Issuer(f'http://127.0.0.1:{args.port}', args.client_id, secret, json.loads(args.identities))
    server = http.server.ThreadingHTTPServer(('127.0.0.1', args.port), handler_for(issuer))
    signal.signal(signal.SIGTERM, lambda *_: (threading.Thread(target=server.shutdown).start()))
    server.serve_forever()
    sys.exit(0)


if __name__ == '__main__':
    main()
