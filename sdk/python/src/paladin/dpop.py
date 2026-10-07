"""Proof of possession for key-bound capabilities (RFC 9449 DPoP).

A capability issued with ``confirmation_jkt = dpop_thumbprint(key.public_key())``
is refused unless each request carries a fresh proof signed by ``key``: a
copy of the token is useless to anyone without the key. Pass the key as
``Client(..., capability=token, dpop_key=key)``.

Keys are ``cryptography``'s Ed25519 or ECDSA P-256 private keys; install the
``dpop`` extra (``pip install paladin-sdk[dpop]``).
"""

from __future__ import annotations

import base64
import hashlib
import json
import secrets
import time
from typing import Any

from connectrpc.request import RequestContext

HEADER_DPOP = "DPoP"
_JTI_BYTES = 16


def _b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def _crypto() -> Any:
    try:
        from cryptography.hazmat.primitives import hashes, serialization
        from cryptography.hazmat.primitives.asymmetric import ec, ed25519, utils
    except ImportError as err:  # pragma: no cover - exercised without the extra
        raise ImportError(
            "DPoP needs the 'cryptography' package: pip install paladin-sdk[dpop]"
        ) from err
    return hashes, serialization, ec, ed25519, utils


def _jwk(public_key: Any) -> dict[str, str]:
    _, serialization, ec, ed25519, _ = _crypto()
    if isinstance(public_key, ed25519.Ed25519PublicKey):
        raw = public_key.public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        return {"kty": "OKP", "crv": "Ed25519", "x": _b64(raw)}
    if isinstance(public_key, ec.EllipticCurvePublicKey) and isinstance(
        public_key.curve, ec.SECP256R1
    ):
        nums = public_key.public_numbers()
        return {
            "kty": "EC",
            "crv": "P-256",
            "x": _b64(nums.x.to_bytes(32, "big")),
            "y": _b64(nums.y.to_bytes(32, "big")),
        }
    raise TypeError(f"DPoP keys are Ed25519 or ECDSA P-256, not {type(public_key).__name__}")


def dpop_thumbprint(public_key: Any) -> str:
    """The RFC 7638 thumbprint of ``public_key``: the ``confirmation_jkt`` to
    issue or delegate a capability with, binding it to the holder of the key."""
    jwk = _jwk(public_key)
    # RFC 7638: the required members only, lexicographic order, no whitespace.
    canonical = json.dumps(dict(sorted(jwk.items())), separators=(",", ":"))
    return _b64(hashlib.sha256(canonical.encode()).digest())


def dpop_proof(key: Any, method: str, url: str, token: str, now: float | None = None) -> str:
    """A proof that the caller holds ``key``, for one ``method`` ``url`` request
    presenting capability ``token``."""
    hashes, _, ec, ed25519, utils = _crypto()
    jwk = _jwk(key.public_key())
    alg = "EdDSA" if jwk["kty"] == "OKP" else "ES256"
    header = {"typ": "dpop+jwt", "alg": alg, "jwk": jwk}
    claims = {
        "jti": _b64(secrets.token_bytes(_JTI_BYTES)),
        "htm": method.upper(),
        "htu": url.split("?", 1)[0].split("#", 1)[0],
        "iat": int(time.time() if now is None else now),
        "ath": _b64(hashlib.sha256(token.encode()).digest()),
    }
    signing_input = f"{_b64(json.dumps(header).encode())}.{_b64(json.dumps(claims).encode())}"
    if isinstance(key, ed25519.Ed25519PrivateKey):
        sig = key.sign(signing_input.encode())
    else:
        r, s = utils.decode_dss_signature(
            key.sign(signing_input.encode(), ec.ECDSA(hashes.SHA256()))
        )
        sig = r.to_bytes(32, "big") + s.to_bytes(32, "big")  # JWS ES256 is r || s, not DER
    return f"{signing_input}.{_b64(sig)}"


def _stamp(key: Any, base_url: str, header_capability: str, ctx: RequestContext) -> None:
    headers = ctx.request_headers
    token = headers.get(header_capability)
    if not token:
        return
    method = ctx.method
    url = f"{base_url}/{method.service_name}/{method.name}"
    headers[HEADER_DPOP] = dpop_proof(key, "POST", url, token)


class DPoPSync:
    """Signs a proof for each call, each attempt of a retried one included —
    the server refuses a proof it has already seen."""

    def __init__(self, key: Any, base_url: str, header_capability: str) -> None:
        _jwk(key.public_key())  # refuse an unsupported key up front
        self._key, self._base_url, self._header = key, base_url, header_capability

    def on_start_sync(self, ctx: RequestContext) -> None:
        _stamp(self._key, self._base_url, self._header, ctx)

    def on_end_sync(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


class DPoPAsync:
    """The async ``DPoPSync``."""

    def __init__(self, key: Any, base_url: str, header_capability: str) -> None:
        _jwk(key.public_key())
        self._key, self._base_url, self._header = key, base_url, header_capability

    async def on_start(self, ctx: RequestContext) -> None:
        _stamp(self._key, self._base_url, self._header, ctx)

    async def on_end(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None
