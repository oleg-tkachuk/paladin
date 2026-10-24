"""The Biscuit ``paladin.testing`` seals for a capability, and the reading of a
narrowed copy of it, as the server's library reads one.

The Biscuit is a real one, signed by ``biscuit_auth`` under a root key of the
fake's own, so ``paladin.attenuate`` narrows it. Its authority block seals, as
the server's does, a token naming that root key in ``limes_bsk``; the fake's
token is its own and unsigned, and nothing but the fake reads it. A copy's
blocks are folded as limes folds them — each a set of ``limes_*`` facts,
narrowing what came before or refusing the whole token — with limes's words
for each refusal, which ``sdk/testdata/biscuit_attenuations.json`` holds to the
server's. A copy's own request and budget limits are read and not counted.
"""

from __future__ import annotations

import base64
import importlib.util
import json
from collections.abc import Callable, Sequence
from dataclasses import dataclass, replace
from datetime import datetime
from typing import Any

from paladin.biscuit import (
    FACT_BIND,
    FACT_CAPABILITY,
    FACT_EXPIRES,
    FACT_MAX_BUDGET_NANOS,
    FACT_MAX_REQUESTS,
    FACT_OP,
    FACT_PLANE,
    FACT_RESOURCE_PREFIX,
    FACT_RESOURCE_URI,
    ROOT_CLAIM,
)

AVAILABLE = importlib.util.find_spec("biscuit_auth") is not None
"""Whether the ``biscuit`` extra is installed; without it the fake answers
with a stand-in instead."""

_JWT_SEPARATOR = "."
_PADDING = "="
_STATEMENT = ";"
_PARAM = "v"
# What the fake's sealed token says it is: nothing verifies its signature,
# which it does not have.
_SEALED_HEADER = {"alg": "none", "typ": "JWT"}
_CAPABILITY_CLAIM = "jti"

# limes's words, which the server sends without its "limes: " prefix.
_INVALID_ATTENUATION = "invalid Biscuit attenuation"
_TOO_WIDE = "child wider than parent"
_NOT_SUPPORTED = "rules and checks are not supported; use the limes_* facts"
EXPIRED = "expired"


class Refusal(Exception):
    """A copy the server's library refuses: ``attenuation`` for its blocks,
    otherwise its lifetime; ``message`` in limes's words."""

    def __init__(self, message: str, *, attenuation: bool) -> None:
        super().__init__(message)
        self.message, self.attenuation = message, attenuation


@dataclass(frozen=True)
class Grant:
    """What a capability, or a copy of it, allows."""

    ops: Sequence[str]
    resource_prefixes: Sequence[str]
    resource_uris: Sequence[str]
    audience: Sequence[str]
    expires_at: datetime

    @property
    def restricts_resources(self) -> bool:
        return bool(self.resource_prefixes or self.resource_uris)


def _b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(_PADDING.encode()).decode()


def _b64decode(data: str) -> bytes:
    return base64.urlsafe_b64decode(data + _PADDING * (-len(data) % 4))


class Sealer:
    """Seals the fake's capabilities and opens copies of them."""

    def __init__(self) -> None:
        import biscuit_auth

        self._ba: Any = biscuit_auth
        self._roots: dict[str, Any] = {}

    def seal(self, capability_id: str) -> str:
        """The Biscuit of the fake's capability ``capability_id``."""
        ba = self._ba
        root = ba.KeyPair()
        claims = {ROOT_CLAIM: _b64(root.public_key.to_bytes()), _CAPABILITY_CLAIM: capability_id}
        sealed = _JWT_SEPARATOR.join(
            (_b64(json.dumps(_SEALED_HEADER).encode()), _b64(json.dumps(claims).encode()), "")
        )
        builder = ba.BiscuitBuilder(f"{FACT_CAPABILITY}({{{_PARAM}}})", {_PARAM: sealed})
        self._roots[capability_id] = root.public_key
        return builder.build(root.private_key).to_base64().rstrip(_PADDING)

    def capability_of(self, token: str) -> str | None:
        """The id of the fake's capability ``token`` is a copy of, read from
        its authority block; None for a token that is none of its Biscuits."""
        ba = self._ba
        try:
            source = ba.UnverifiedBiscuit.from_base64(token).block_source(0)
            authority = ba.Fact(source.strip().rstrip(_STATEMENT))
            payload = authority.terms[0].split(_JWT_SEPARATOR)[1]
            capability: str = json.loads(_b64decode(payload))[_CAPABILITY_CLAIM]
        except (
            ba.BiscuitSerializationError,
            ba.BiscuitValidationError,
            ba.DataLogError,
            ValueError,
            KeyError,
            IndexError,
        ):
            return None
        return capability if capability in self._roots else None

    def open(self, token: str, capability_id: str, granted: Grant, now: datetime) -> Grant:
        """``granted`` as the blocks of ``token``, a copy of the capability's
        Biscuit, narrow it; raise ``Refusal`` as the library refuses it."""
        ba = self._ba
        try:
            biscuit = ba.Biscuit.from_base64(token, self._roots[capability_id])
        except Exception as err:
            raise Refusal(f"invalid Biscuit: {err}", attenuation=True) from err
        current = granted
        for i in range(1, biscuit.block_count()):
            try:
                current = _fold(ba, biscuit.block_source(i), current)
            except _Wide as err:
                raise Refusal(
                    f"{_INVALID_ATTENUATION}: block {i}: {err}", attenuation=True
                ) from None
        if not now < current.expires_at:
            raise Refusal(EXPIRED, attenuation=False)
        return current


class _Wide(Exception):
    """One block the library refuses, in its words."""


def _fold(ba: Any, source: str, current: Grant) -> Grant:
    """``current`` narrowed by one block's facts, as limes folds them."""
    ops: list[str] = []
    prefixes: list[str] = []
    uris: list[str] = []
    planes: list[str] = []
    resources = False
    expires: datetime | None = None
    for statement in filter(None, (s.strip() for s in source.split(_STATEMENT))):
        try:
            fact = ba.Fact(statement)
        except ba.DataLogError:
            raise _Wide(_NOT_SUPPORTED) from None
        term = fact.terms[0] if len(fact.terms) == 1 else None
        name = fact.name
        if name == FACT_OP:
            ops.append(term)
        elif name in (FACT_RESOURCE_PREFIX, FACT_RESOURCE_URI):
            resources = True
            (prefixes if name == FACT_RESOURCE_PREFIX else uris).append(term)
        elif name == FACT_PLANE:
            planes.append(term)
        elif name == FACT_EXPIRES:
            if expires is None or term < expires:
                expires = term
        elif name in (FACT_BIND, FACT_MAX_REQUESTS, FACT_MAX_BUDGET_NANOS):
            continue  # read by the server, not counted here
        else:
            from paladin.testing import _go_quote

            raise _Wide(f"fact {_go_quote(name)} is not part of the attenuation vocabulary")
    child = replace(
        current,
        ops=ops or current.ops,
        resource_prefixes=prefixes if resources else current.resource_prefixes,
        resource_uris=uris if resources else current.resource_uris,
        audience=planes or current.audience,
        expires_at=expires
        if expires is not None and expires < current.expires_at
        else current.expires_at,
    )
    _narrows(current, child)
    return child


def _narrows(parent: Grant, child: Grant) -> None:
    """Raise ``_Wide`` when ``child`` allows what ``parent`` does not, in the
    order and the words of limes's Narrows."""
    from paladin.testing import _go_list, _go_quote, _match_resource

    if not set(child.audience) <= set(parent.audience):
        raise _Wide(
            f"{_TOO_WIDE}: audience {_go_list(child.audience)} widens parent {_go_list(parent.audience)}"
        )
    for op in child.ops:
        if op not in parent.ops:
            raise _Wide(
                f"{_TOO_WIDE}: child op {_go_quote(op)} not in parent ops {_go_list(parent.ops)}"
            )
    if not parent.restricts_resources:
        return
    if not child.restricts_resources:
        raise _Wide(
            f"{_TOO_WIDE}: parent restricts resources (prefixes {_go_list(parent.resource_prefixes)}, "
            f"URIs {_go_list(parent.resource_uris)}), child unrestricted"
        )
    covers: Callable[[str], bool] = lambda c: any(
        _match_resource(p, c) for p in parent.resource_prefixes
    )
    for c in child.resource_prefixes:
        if not covers(c):
            raise _Wide(
                f"{_TOO_WIDE}: child prefix {_go_quote(c)} not under any parent prefix "
                f"{_go_list(parent.resource_prefixes)}"
            )
    for c in child.resource_uris:
        if c not in parent.resource_uris and not covers(c):
            raise _Wide(f"{_TOO_WIDE}: child URI {_go_quote(c)} not in parent allowance")
