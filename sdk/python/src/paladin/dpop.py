"""Proof of possession for key-bound capabilities (RFC 9449 DPoP).

A capability issued with ``confirmation_jkt = dpop_thumbprint(key.public_key())``
is refused unless each request carries a fresh proof signed by ``key``: a
copy of the token is useless to anyone without the key. Pass the key as
``Client(..., capability=token, dpop_key=key)``.

Keys are ``cryptography``'s Ed25519 or ECDSA P-256 private keys; install the
``dpop`` extra (``uv add 'paladin-sdk[dpop]'``).

A server that asks for nonces (RFC 9449 §8) sends one with each response; the
client keeps the latest and signs it into the next proof. A unary call refused
for want of a fresh one is sent once more with it. A stream signs the latest
nonce the client holds and is not resent.
"""

from __future__ import annotations

import base64
import hashlib
import json
import secrets
import threading
import time
from collections.abc import Awaitable, Callable
from typing import Any, TypeVar

from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.request import RequestContext

from paladin.relay import Captured, captured

REQ = TypeVar("REQ")
RES = TypeVar("RES")

EXTRA_HINT = "DPoP needs the 'dpop' extra: uv add 'paladin-sdk[dpop]'"
"""What signing a proof raises, as an ``ImportError``, without the extra."""

HEADER_DPOP = "DPoP"
# The nonce the server wants in the next proof, and the challenge of a refused
# credential (RFC 9449 §8).
HEADER_DPOP_NONCE = "DPoP-Nonce"
HEADER_WWW_AUTHENTICATE = "WWW-Authenticate"
# The error a DPoP challenge names when the proof lacked the server's nonce.
NONCE_CHALLENGE = "use_dpop_nonce"
_JTI_BYTES = 16
# The proof algorithms, as RFC 9864 names them: Ed25519 is the fully specified
# name for what the deprecated "EdDSA" covered with this key type.
ALG_ED25519 = "Ed25519"
ALG_ES256 = "ES256"


def _b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def _crypto() -> Any:
    try:
        from cryptography.hazmat.primitives import hashes, serialization
        from cryptography.hazmat.primitives.asymmetric import ec, ed25519, utils
    except ImportError as err:  # pragma: no cover - exercised without the extra
        raise ImportError(EXTRA_HINT) from err
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


def dpop_proof(
    key: Any, method: str, url: str, token: str, now: float | None = None, nonce: str = ""
) -> str:
    """A proof that the caller holds ``key``, for one ``method`` ``url`` request
    presenting capability ``token``, carrying the server's ``nonce`` if any."""
    hashes, _, ec, ed25519, utils = _crypto()
    jwk = _jwk(key.public_key())
    alg = ALG_ED25519 if jwk["kty"] == "OKP" else ALG_ES256
    header = {"typ": "dpop+jwt", "alg": alg, "jwk": jwk}
    claims = {
        "jti": _b64(secrets.token_bytes(_JTI_BYTES)),
        "htm": method.upper(),
        "htu": url.split("?", 1)[0].split("#", 1)[0],
        "iat": int(time.time() if now is None else now),
        "ath": _b64(hashlib.sha256(token.encode()).digest()),
    }
    if nonce:
        claims["nonce"] = nonce
    signing_input = f"{_b64(json.dumps(header).encode())}.{_b64(json.dumps(claims).encode())}"
    if isinstance(key, ed25519.Ed25519PrivateKey):
        sig = key.sign(signing_input.encode())
    else:
        r, s = utils.decode_dss_signature(
            key.sign(signing_input.encode(), ec.ECDSA(hashes.SHA256()))
        )
        sig = r.to_bytes(32, "big") + s.to_bytes(32, "big")  # JWS ES256 is r || s, not DER
    return f"{signing_input}.{_b64(sig)}"


class NonceState:
    """The latest nonce the server sent, shared by every call of a client."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._nonce = ""

    def get(self) -> str:
        with self._lock:
            return self._nonce

    def adopt(self, meta: Captured) -> None:
        nonce = meta.get(HEADER_DPOP_NONCE)
        if nonce:
            with self._lock:
                self._nonce = nonce


def _challenged(err: ConnectError, meta: Captured) -> bool:
    """Whether ``err`` refused the proof for want of the server's nonce, and
    the response carries one to retry with."""
    challenge = meta.get(HEADER_WWW_AUTHENTICATE) or ""
    return (
        err.code == Code.UNAUTHENTICATED
        and bool(meta.get(HEADER_DPOP_NONCE))
        and NONCE_CHALLENGE in challenge
    )


def _stamp(
    key: Any, base_url: str, header_capability: str, nonces: NonceState, ctx: RequestContext
) -> None:
    headers = ctx.request_headers
    token = headers.get(header_capability)
    if not token:
        return
    method = ctx.method
    url = f"{base_url}/{method.service_name}/{method.name}"
    headers[HEADER_DPOP] = dpop_proof(key, "POST", url, token, nonce=nonces.get())


class DPoPNonceSync:
    """Keeps the nonce each response brings, and sends a unary call refused
    for want of a fresh one once more; the proof signer inside it signs the
    second attempt with the new nonce."""

    def __init__(self, nonces: NonceState) -> None:
        self._nonces = nonces

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        with captured() as meta:
            try:
                return call_next(request, ctx)
            except ConnectError as err:
                if not _challenged(err, meta):
                    raise
            finally:
                self._nonces.adopt(meta)
        with captured() as meta:
            try:
                return call_next(request, ctx)
            finally:
                self._nonces.adopt(meta)


class DPoPNonceAsync:
    """The async ``DPoPNonceSync``."""

    def __init__(self, nonces: NonceState) -> None:
        self._nonces = nonces

    async def intercept_unary(
        self,
        call_next: Callable[[REQ, RequestContext], Awaitable[RES]],
        request: REQ,
        ctx: RequestContext,
    ) -> RES:
        with captured() as meta:
            try:
                return await call_next(request, ctx)
            except ConnectError as err:
                if not _challenged(err, meta):
                    raise
            finally:
                self._nonces.adopt(meta)
        with captured() as meta:
            try:
                return await call_next(request, ctx)
            finally:
                self._nonces.adopt(meta)


class DPoPSync:
    """Signs a proof for each call, each attempt of a retried one included —
    the server refuses a proof it has already seen."""

    def __init__(
        self, key: Any, base_url: str, header_capability: str, nonces: NonceState
    ) -> None:
        _jwk(key.public_key())  # refuse an unsupported key up front
        self._key, self._base_url, self._header = key, base_url, header_capability
        self._nonces = nonces

    def on_start_sync(self, ctx: RequestContext) -> None:
        _stamp(self._key, self._base_url, self._header, self._nonces, ctx)

    def on_end_sync(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


class DPoPAsync:
    """The async ``DPoPSync``."""

    def __init__(
        self, key: Any, base_url: str, header_capability: str, nonces: NonceState
    ) -> None:
        _jwk(key.public_key())
        self._key, self._base_url, self._header = key, base_url, header_capability
        self._nonces = nonces

    async def on_start(self, ctx: RequestContext) -> None:
        _stamp(self._key, self._base_url, self._header, self._nonces, ctx)

    async def on_end(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None
