"""The test issuer enforces client, redirect, PKCE and one-time codes, and signs verifiable tokens."""
import base64
import hashlib
import json
import unittest
import urllib.parse

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import padding, rsa

import oidc_issuer


def b64decode(text: str) -> bytes:
    return base64.urlsafe_b64decode(text + '=' * (-len(text) % 4))


class IssuerTests(unittest.TestCase):
    def setUp(self):
        self.issuer = oidc_issuer.Issuer('http://127.0.0.1:9', 'client', 'secret',
                                         [{'sub': 'a', 'email': 'a@t.test'}, {'sub': 'b', 'email': 'b@t.test'}])
        self.verifier = 'v' * 50
        self.challenge = oidc_issuer.b64url(hashlib.sha256(self.verifier.encode()).digest())

    def code(self):
        location = self.issuer.authorize({'client_id': 'client', 'response_type': 'code', 'state': 's',
                                          'nonce': 'n', 'code_challenge': self.challenge,
                                          'code_challenge_method': 'S256', 'redirect_uri': 'http://127.0.0.1:1/cb'})
        return dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(location).query))['code']

    def exchange(self, code, verifier=None, client=('client', 'secret')):
        return self.issuer.token({'grant_type': 'authorization_code', 'code': code, 'redirect_uri': 'http://127.0.0.1:1/cb',
                                  'code_verifier': verifier or self.verifier}, client)

    def test_identities_are_issued_in_order_with_a_verifiable_signature(self):
        tokens = [self.exchange(self.code())['id_token'] for _ in range(2)]
        claims = [json.loads(b64decode(t.split('.')[1])) for t in tokens]
        self.assertEqual([c['sub'] for c in claims], ['a', 'b'])
        self.assertTrue(all(c['email_verified'] and c['nonce'] == 'n' and c['aud'] == 'client' for c in claims))
        jwk = self.issuer.jwks()['keys'][0]
        public = rsa.RSAPublicNumbers(int.from_bytes(b64decode(jwk['e']), 'big'),
                                      int.from_bytes(b64decode(jwk['n']), 'big')).public_key()
        head, body, signature = tokens[0].split('.')
        public.verify(b64decode(signature), f'{head}.{body}'.encode(), padding.PKCS1v15(), hashes.SHA256())
        with self.assertRaisesRegex(ValueError, 'no identity left'):
            self.code()

    def test_refuses_bad_verifier_reused_code_and_wrong_client(self):
        code = self.code()
        with self.assertRaisesRegex(ValueError, 'invalid client'):
            self.exchange(code, client=('client', 'wrong'))
        with self.assertRaisesRegex(ValueError, 'PKCE'):
            self.exchange(code, verifier='x' * 50)
        with self.assertRaisesRegex(ValueError, 'unknown, reused'):
            self.exchange(code)


if __name__ == '__main__':
    unittest.main()
