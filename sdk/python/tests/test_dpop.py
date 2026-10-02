from __future__ import annotations

import base64
import hashlib
import json

import pytest
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec, ed25519, utils

from paladin import HEADER_CAPABILITY, Client, Retry
from paladin.dpop import HEADER_DPOP, dpop_proof, dpop_thumbprint
from paladin.iam.v1 import health_service_pb2
from paladin.iam.v1.health_service_connect import HealthServiceClientSync

FAST = Retry(attempts=3, base_delay=0.001, max_delay=0.001)
GET_VERSION = "/paladin.iam.v1.HealthService/GetVersion"


def _unb64(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def _decode(proof: str) -> tuple[dict, dict, bytes, bytes]:
    h, c, s = proof.split(".")
    return json.loads(_unb64(h)), json.loads(_unb64(c)), f"{h}.{c}".encode(), _unb64(s)


def test_thumbprint_matches_rfc_8037() -> None:
    # RFC 8037 A.3: the thumbprint of the Ed25519 key in A.2.
    key = ed25519.Ed25519PublicKey.from_public_bytes(
        _unb64("11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo")
    )
    assert dpop_thumbprint(key) == "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"


@pytest.mark.parametrize(
    "make", [ed25519.Ed25519PrivateKey.generate, lambda: ec.generate_private_key(ec.SECP256R1())]
)
def test_proof_is_signed_by_the_key_over_the_request(make) -> None:  # type: ignore[no-untyped-def]
    key = make()
    proof = dpop_proof(
        key, "post", "https://data.example.com/svc/M?x=1", "the-token", now=1_800_000_000
    )
    header, claims, signed, sig = _decode(proof)

    assert header["typ"] == "dpop+jwt"
    assert claims["htm"] == "POST"
    assert claims["htu"] == "https://data.example.com/svc/M"
    assert claims["iat"] == 1_800_000_000
    assert (
        claims["ath"]
        == base64.urlsafe_b64encode(hashlib.sha256(b"the-token").digest()).rstrip(b"=").decode()
    )
    # The header's key is the signer's, and its thumbprint the binding.
    assert header["jwk"]["kty"] in ("OKP", "EC")
    pub = key.public_key()
    if isinstance(key, ed25519.Ed25519PrivateKey):
        assert header["alg"] == "EdDSA"
        pub.verify(sig, signed)
    else:
        assert header["alg"] == "ES256"
        assert len(sig) == 64
        der = utils.encode_dss_signature(
            int.from_bytes(sig[:32], "big"), int.from_bytes(sig[32:], "big")
        )
        pub.verify(der, signed, ec.ECDSA(hashes.SHA256()))


def test_unsupported_key_is_refused_up_front() -> None:
    with pytest.raises(TypeError):
        Client("https://data.example.com", dpop_key=ec.generate_private_key(ec.SECP384R1()))


def test_each_attempt_carries_its_own_proof(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    key = ed25519.Ed25519PrivateKey.generate()
    client = Client(server.url, capability="cap-token", dpop_key=key, retry=FAST)
    HealthServiceClientSync(client.base_url, interceptors=client.interceptors()).get_version(
        health_service_pb2.GetVersionRequest()
    )

    proofs = [h[HEADER_DPOP.lower()] for h in server.recorder.headers]
    assert len(proofs) == 2 and proofs[0] != proofs[1], (
        "a retry reused a proof the server would refuse"
    )
    _, claims, _, _ = _decode(proofs[-1])
    assert claims["htu"] == server.url.rstrip("/") + GET_VERSION
    assert server.recorder.last[HEADER_CAPABILITY.lower()] == "cap-token"


def test_no_proof_without_a_capability(server) -> None:  # type: ignore[no-untyped-def]
    client = Client(server.url, bearer_token="t", dpop_key=ed25519.Ed25519PrivateKey.generate())
    HealthServiceClientSync(client.base_url, interceptors=client.interceptors()).get_version(
        health_service_pb2.GetVersionRequest()
    )
    assert HEADER_DPOP.lower() not in server.recorder.last
