"""One capability per key, for a service that acts for many callers.

A service minting each caller a short-lived capability keeps it here until
``CAPABILITY_REFRESH_MARGIN`` before it expires, and hands ``token`` to the
client's ``capability_source``::

    cache = CapabilityCache(mint)
    client = Client(capability_source=lambda: cache.token(tenant.get()))

Concurrent callers for one key wait for a single mint; a mint that fails is
not cached, so the next call mints again.
"""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

CAPABILITY_REFRESH_MARGIN = timedelta(seconds=30)
"""How long before its expiry a cached capability is replaced, so a call does
not leave with one that lapses in flight."""

Mint = Callable[[str], tuple[str, datetime]]
"""Issues a capability for a key — a tenant, a resource prefix — and returns
it with its expiry, an aware datetime."""
AsyncMint = Callable[[str], Awaitable[tuple[str, datetime]]]


def _utcnow() -> datetime:
    return datetime.now(UTC)


class NoCapabilityKeyError(ValueError):
    """``token`` was asked for an empty key."""


@dataclass
class _Entry:
    token: str
    expires: datetime


class _Entries:
    """The cached capabilities, shared by the sync and async kinds."""

    def __init__(self, margin: timedelta, clock: Callable[[], datetime]) -> None:
        self.margin = margin
        self.clock = clock
        self.entries: dict[str, _Entry] = {}

    def fresh(self, key: str) -> str | None:
        if not key:
            raise NoCapabilityKeyError("capability cache key is empty")
        entry = self.entries.get(key)
        if entry and self.clock() + self.margin < entry.expires:
            return entry.token
        return None


class CapabilityCache:
    """A capability per key, minted by ``mint``. Safe to share between threads."""

    def __init__(
        self,
        mint: Mint,
        *,
        refresh_margin: timedelta = CAPABILITY_REFRESH_MARGIN,
        clock: Callable[[], datetime] = _utcnow,
    ) -> None:
        self._mint = mint
        self._state = _Entries(refresh_margin, clock)
        self._lock = threading.Lock()
        self._minting: dict[str, threading.Lock] = {}

    def token(self, key: str) -> str:
        """A capability for ``key`` that outlives the refresh margin."""
        with self._lock:
            cached = self._state.fresh(key)
            if cached:
                return cached
            mint_lock = self._minting.setdefault(key, threading.Lock())
        with mint_lock:
            # Another thread may have minted it while this one waited.
            with self._lock:
                cached = self._state.fresh(key)
            if cached:
                return cached
            token, expires = self._mint(key)
            with self._lock:
                self._state.entries[key] = _Entry(token, expires)
            return token

    def invalidate(self, key: str) -> None:
        """Drop ``key``'s capability — one the server refused; the next
        ``token`` mints another."""
        with self._lock:
            self._state.entries.pop(key, None)


class AsyncCapabilityCache:
    """``CapabilityCache`` for an async ``mint``. For one event loop."""

    def __init__(
        self,
        mint: AsyncMint,
        *,
        refresh_margin: timedelta = CAPABILITY_REFRESH_MARGIN,
        clock: Callable[[], datetime] = _utcnow,
    ) -> None:
        self._mint = mint
        self._state = _Entries(refresh_margin, clock)
        self._minting: dict[str, asyncio.Lock] = {}

    async def token(self, key: str) -> str:
        """A capability for ``key`` that outlives the refresh margin."""
        cached = self._state.fresh(key)
        if cached:
            return cached
        async with self._minting.setdefault(key, asyncio.Lock()):
            cached = self._state.fresh(key)
            if cached:
                return cached
            token, expires = await self._mint(key)
            self._state.entries[key] = _Entry(token, expires)
            return token

    def invalidate(self, key: str) -> None:
        """Drop ``key``'s capability; the next ``token`` mints another."""
        self._state.entries.pop(key, None)
