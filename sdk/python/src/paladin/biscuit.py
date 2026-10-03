"""Offline attenuation of a capability's Biscuit form.

``CapabilityService.Issue`` and ``Delegate`` return ``biscuit`` beside
``token``: the same capability as a Biscuit v3 token, which its holder can
narrow with no key and no call to the server. ``attenuate`` appends a block
that restricts it; nobody, the holder included, can remove the block again,
so an agent can hand a sub-agent a strictly smaller token.

The block speaks only the server's vocabulary (``sdk/testdata/
biscuit_vocabulary.json``): the operations, resources, planes and expiry the
token keeps, and a key to bind it to. A restriction that would widen the token
is not refused here but by the server, which then refuses the whole token.

Needs ``biscuit-python``: ``pip install paladin-sdk[biscuit]``.
"""

from __future__ import annotations

import base64
import binascii
import json
from collections.abc import Iterable
from datetime import datetime, timezone
from typing import Any

# The attenuation vocabulary; tests/test_biscuit.py checks each name against
# the shared spec the server's tests read too.
FACT_CAPABILITY = "paladin_capability"
FACT_OP = "paladin_op"
FACT_RESOURCE_PREFIX = "paladin_resource_prefix"
FACT_RESOURCE_URI = "paladin_resource_uri"
FACT_PLANE = "paladin_plane"
FACT_EXPIRES = "paladin_expires"
FACT_BIND = "paladin_bind"
# The sealed JWT's claim holding the public key that roots the Biscuit.
ROOT_CLAIM = "paladin_bsk"
# A JWK thumbprint is a base64url SHA-256 digest.
THUMBPRINT_BYTES = 32

_JWT_SEPARATOR = "."
_JWT_PAYLOAD = 1
_JWT_SEGMENTS = 3
_PADDING = "="
_PARAM = "v"


def _biscuit() -> Any:
    try:
        import biscuit_auth
    except ImportError as err:  # pragma: no cover - exercised without the extra
        raise ImportError(
            "Biscuit attenuation needs 'biscuit-python': pip install paladin-sdk[biscuit]"
        ) from err
    return biscuit_auth


def _b64decode(data: str) -> bytes:
    return base64.urlsafe_b64decode(data + _PADDING * (-len(data) % 4))


def _strings(name: str, values: Iterable[str] | None, *, non_empty: bool = False) -> list[str]:
    if values is None:
        return []
    if isinstance(values, str):
        raise TypeError(f"{name} takes a list of strings, not one string")
    out = list(values)
    for v in out:
        if not isinstance(v, str):
            raise TypeError(f"{name} takes strings, got {type(v).__name__}")
        if non_empty and not v:
            raise ValueError(f"{name} takes non-empty strings")
    return out


def _thumbprint(jkt: str) -> str:
    try:
        raw = _b64decode(jkt)
    except (binascii.Error, ValueError):
        raw = b""
    if _PADDING in jkt or len(raw) != THUMBPRINT_BYTES:
        raise ValueError(f"bind_jkt {jkt!r} is not a base64url SHA-256 JWK thumbprint")
    return jkt


def _root_key(ba: Any, token: str) -> Any:
    """The public key rooting ``token``'s chain, read from the JWT its
    authority block seals. Not trusted here: the chain is checked against it
    only so that a corrupt token fails now rather than at the server, which
    verifies the JWT and the chain itself."""
    try:
        source = ba.UnverifiedBiscuit.from_base64(token).block_source(0)
        authority = ba.Fact(source.strip().rstrip(";"))
    except Exception as err:
        raise ValueError(f"not a Paladin Biscuit token: {err}") from err
    terms = authority.terms
    if authority.name != FACT_CAPABILITY or len(terms) != 1 or not isinstance(terms[0], str):
        raise ValueError("not a Paladin Biscuit token: the authority is not a sealed capability")
    segments = terms[0].split(_JWT_SEPARATOR)
    try:
        if len(segments) != _JWT_SEGMENTS:
            raise ValueError("not a JWT")
        claims = json.loads(_b64decode(segments[_JWT_PAYLOAD]))
        root = _b64decode(claims[ROOT_CLAIM])
        return ba.PublicKey.from_bytes(root, ba.Algorithm.Ed25519)
    except Exception as err:
        raise ValueError(f"not a Paladin Biscuit token: the sealed capability: {err}") from err


def attenuate(
    token: str,
    *,
    ops: Iterable[str] | None = None,
    resource_prefixes: Iterable[str] | None = None,
    resource_uris: Iterable[str] | None = None,
    planes: Iterable[str] | None = None,
    expires_at: datetime | None = None,
    bind_jkt: str | None = None,
) -> str:
    """``token`` narrowed by one appended block, as a new token; ``token``
    itself keeps working as before.

    Each argument left ``None`` leaves that dimension as it is; a set one
    replaces it, and must be within what the token already allows or the
    server refuses the whole token. ``ops`` are operation names (``"get"``,
    ``"list"``, …) and ``planes`` audiences (``"data"``, ``"mcp"``, …).
    Setting ``resource_prefixes`` or ``resource_uris`` replaces both.
    ``expires_at`` (timezone-aware, kept to the second) shortens the
    lifetime. ``bind_jkt`` — ``dpop_thumbprint(key.public_key())`` — binds
    the token to a key; only an unbound token can be bound offline.

    Raises ``ValueError`` for a token that is not a Paladin Biscuit, and
    ``TypeError``/``ValueError`` for malformed arguments.
    """
    if not token or _JWT_SEPARATOR in token:
        raise ValueError("not a Biscuit token: pass the 'biscuit' field, not the JWT 'token'")
    facts: list[tuple[str, Any]] = []
    facts += [(FACT_OP, v) for v in _strings("ops", ops)]
    facts += [
        (FACT_RESOURCE_PREFIX, v)
        for v in _strings("resource_prefixes", resource_prefixes, non_empty=True)
    ]
    facts += [
        (FACT_RESOURCE_URI, v) for v in _strings("resource_uris", resource_uris, non_empty=True)
    ]
    facts += [(FACT_PLANE, v) for v in _strings("planes", planes)]
    if expires_at is not None:
        if not isinstance(expires_at, datetime):
            raise TypeError(f"expires_at takes a datetime, got {type(expires_at).__name__}")
        if expires_at.tzinfo is None:
            raise ValueError("expires_at must be timezone-aware")
        facts.append((FACT_EXPIRES, expires_at.astimezone(timezone.utc).replace(microsecond=0)))
    if bind_jkt is not None:
        facts.append((FACT_BIND, _thumbprint(bind_jkt)))

    ba = _biscuit()
    root = _root_key(ba, token)
    try:
        biscuit = ba.Biscuit.from_base64(token, root)
    except Exception as err:
        raise ValueError(f"not a Paladin Biscuit token: {err}") from err
    block = ba.BlockBuilder("")
    for name, value in facts:
        block.add_fact(ba.Fact(f"{name}({{{_PARAM}}})", {_PARAM: value}))
    return biscuit.append(block).to_base64().rstrip(_PADDING)
