"""Offline attenuation of a capability's Biscuit form.

``CapabilityService.Issue`` and ``Delegate`` return ``biscuit`` beside
``token``: the same capability as a Biscuit v3 token, which its holder can
narrow with no key and no call to the server. ``attenuate`` appends a block
that restricts it; nobody, the holder included, can remove the block again,
so an agent can hand a sub-agent a strictly smaller token.

The block speaks only the server's vocabulary (``sdk/testdata/
biscuit_vocabulary.json``): the operations, resources, planes and expiry the
token keeps, a key to bind it to, and limits the copy counts on its own. A restriction that would widen the token
is not refused here but by the server, which then refuses the whole token.

Needs ``biscuit-python``: ``uv add 'paladin-sdk[biscuit]'``.
"""

from __future__ import annotations

import base64
import binascii
import json
from collections.abc import Iterable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from enum import StrEnum
from typing import Any

from paladin.admin.v1 import capability_service_pb2
from paladin.auth import Op, Plane

EXTRA_HINT = "Biscuit attenuation needs the 'biscuit' extra: uv add 'paladin-sdk[biscuit]'"
"""What attenuating raises, as an ``ImportError``, without the extra."""

# The attenuation vocabulary; tests/test_biscuit.py checks each name against
# the shared spec the server's tests read too.
FACT_CAPABILITY = "limes_capability"
FACT_OP = "limes_op"
FACT_RESOURCE_PREFIX = "limes_resource_prefix"
FACT_RESOURCE_URI = "limes_resource_uri"
FACT_PLANE = "limes_plane"
FACT_EXPIRES = "limes_expires"
FACT_BIND = "limes_bind"
FACT_MAX_REQUESTS = "limes_max_requests"
FACT_MAX_BUDGET_NANOS = "limes_max_budget_nanos"
# The sealed JWT's claim holding the public key that roots the Biscuit.
ROOT_CLAIM = "limes_bsk"
# A JWK thumbprint is a base64url SHA-256 digest.
THUMBPRINT_BYTES = 32

_JWT_SEPARATOR = "."
_JWT_PAYLOAD = 1
_JWT_SEGMENTS = 3
_PADDING = "="
_PARAM = "v"


class NoBiscuitError(Exception):
    """An Issue or Delegate answer that carries no Biscuit; ``status`` is why."""

    def __init__(self, status: int) -> None:
        self.status = status
        super().__init__(
            f"the capability was issued without a Biscuit: {capability_service_pb2.BiscuitStatus.Name(status)}"
        )


def biscuit_of(resp: capability_service_pb2.CapabilityServiceIssueResponse) -> str:
    """The Biscuit an Issue or Delegate answer carries.

    Raises ``NoBiscuitError`` naming the answer's ``biscuit_status`` when it
    carries none, so a caller that needs one fails where it asks rather than
    passing an empty token on. To be refused before anything is issued
    instead, set ``require_biscuit``. A caller that only presents the
    capability wants ``credential_of``.
    """
    if resp.biscuit_status == capability_service_pb2.BISCUIT_STATUS_ISSUED and resp.biscuit:
        return resp.biscuit
    raise NoBiscuitError(resp.biscuit_status)


class CredentialForm(StrEnum):
    """Which form of a capability a ``Credential`` is. Both are presented the
    same way, as ``capability=``."""

    TOKEN = "token"
    """The capability's JWT."""
    BISCUIT = "biscuit"
    """Its Biscuit, which its holder can also narrow offline with ``attenuate``."""


@dataclass(frozen=True)
class Credential:
    """What to present for a capability an Issue or Delegate answer carries."""

    value: str = field(repr=False)
    """Presented as ``capability=``. Left out of the repr, so a credential
    logged whole does not put the capability in the log."""
    form: CredentialForm
    """Which form ``value`` is."""
    biscuit_status: int
    """The answer's: ``BISCUIT_STATUS_ISSUED`` for a Biscuit, otherwise why the
    token is presented instead."""


class NoCredentialError(Exception):
    """An Issue or Delegate answer that carries neither a token nor a Biscuit."""

    def __init__(self, status: int) -> None:
        self.status = status
        super().__init__(
            "the answer carries no capability to present: "
            f"{capability_service_pb2.BiscuitStatus.Name(status)}"
        )


def credential_of(resp: capability_service_pb2.CapabilityServiceIssueResponse) -> Credential:
    """What to present for the capability an Issue or Delegate answer
    carries: its Biscuit when ``biscuit_status`` is ``ISSUED``, its token
    otherwise, with the status that says why.

    A caller that only presents the capability needs no more; one that must
    narrow it offline asks ``biscuit_of``. Raises ``NoCredentialError`` for an
    answer that carries neither.
    """
    status = resp.biscuit_status
    if resp.biscuit and status == capability_service_pb2.BISCUIT_STATUS_ISSUED:
        return Credential(resp.biscuit, CredentialForm.BISCUIT, status)
    if resp.token:
        return Credential(resp.token, CredentialForm.TOKEN, status)
    raise NoCredentialError(status)


def token_of(resp: capability_service_pb2.CapabilityServiceIssueResponse) -> str:
    """The token an Issue or Delegate answer carries, whatever Biscuit comes
    beside it: for a caller that presents the capability as issued and never
    narrows it offline. Raises ``NoCredentialError`` for an answer without one.
    """
    if resp.token:
        return resp.token
    raise NoCredentialError(resp.biscuit_status)


def _biscuit() -> Any:
    try:
        import biscuit_auth
    except ImportError as err:  # pragma: no cover - exercised without the extra
        raise ImportError(EXTRA_HINT) from err
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


def _ops(ops: Iterable[Op] | None) -> list[Op]:
    """``ops`` as ``Op`` members; a name that is none is a ``ValueError``."""
    out: list[Op] = []
    for v in _strings("ops", ops):
        try:
            out.append(Op(v))
        except ValueError:
            known = ", ".join(op.value for op in Op)
            raise ValueError(f"ops: {v!r} is no operation; the operations are {known}") from None
    return out


def _non_negative(name: str, value: int) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} takes an int, got {type(value).__name__}")
    if value < 0:
        raise ValueError(f"{name} must not be negative")
    return value


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
    ops: Iterable[Op] | None = None,
    resource_prefixes: Iterable[str] | None = None,
    resource_uris: Iterable[str] | None = None,
    planes: Iterable[Plane] | None = None,
    expires_at: datetime | None = None,
    bind_jkt: str | None = None,
    max_requests: int | None = None,
    max_budget_nanos: int | None = None,
) -> str:
    """``token`` narrowed by one appended block, as a new token; ``token``
    itself keeps working as before.

    Each argument left ``None`` leaves that dimension as it is; a set one
    replaces it, and must be within what the token already allows or the
    server refuses the whole token. ``ops`` are ``Op`` members (``Op.GET``,
    ``Op.LIST``, …), and a name that is none is a ``ValueError`` here rather
    than a refused token; ``planes`` are ``Plane`` members (``Plane.DATA``, …).
    Setting ``resource_prefixes`` or ``resource_uris`` replaces both.
    ``expires_at`` (timezone-aware, kept to the second) shortens the
    lifetime. ``bind_jkt`` — ``dpop_thumbprint(key.public_key())`` — binds
    the token to a key; only an unbound token can be bound offline.
    ``max_requests`` and ``max_budget_nanos`` give the new copy limits of its
    own — requests made with it, and its spend in nanos, billionths of the
    capability's unit — counted apart from other copies and within every limit already in
    force. A limit of 0 freezes the copy: its first request and its first
    charge are refused, while the token it came from keeps working.

    Raises ``ValueError`` for a token that is not a Paladin Biscuit, and
    ``TypeError``/``ValueError`` for malformed arguments.
    """
    if not token or _JWT_SEPARATOR in token:
        raise ValueError("not a Biscuit token: pass the 'biscuit' field, not the JWT 'token'")
    facts: list[tuple[str, Any]] = []
    facts += [(FACT_OP, str(v)) for v in _ops(ops)]
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
        facts.append((FACT_EXPIRES, expires_at.astimezone(UTC).replace(microsecond=0)))
    if bind_jkt is not None:
        facts.append((FACT_BIND, _thumbprint(bind_jkt)))
    if max_requests is not None:
        facts.append((FACT_MAX_REQUESTS, _non_negative("max_requests", max_requests)))
    if max_budget_nanos is not None:
        facts.append((FACT_MAX_BUDGET_NANOS, _non_negative("max_budget_nanos", max_budget_nanos)))

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
