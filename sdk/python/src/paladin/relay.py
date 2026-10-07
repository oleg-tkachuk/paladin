"""Response headers for the SDK's interceptors, read at the transport.

A client interceptor of connectrpc sees no response headers, and its own
``ResponseMetadata`` would hide the headers from one the caller opened around
the call. So the SDK reads them where the HTTP response passes: a ``pyqwest``
transport wrapped in ``RelaySyncTransport`` or ``RelayTransport`` hands the
headers of each response to every interceptor waiting on them, and leaves
connectrpc's own ``ResponseMetadata`` to the caller.

``connect``, ``connect_async`` and ``Client.http_client`` build their HTTP
clients over these transports. A caller who brings an ``http_client`` of its
own wraps its transport the same way; without it, typed errors carry no
``server_version`` or ``retry_after`` and retries ignore ``Retry-After``.
"""

from __future__ import annotations

import warnings
from collections.abc import Iterator, Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Any

import pyqwest

_NOT_RELAYED = (
    "paladin: the HTTP client in use does not relay response headers, so errors "
    "carry no server version and retries ignore Retry-After; build it with "
    "Client.http_client() or wrap its transport in paladin.RelaySyncTransport"
)


class Captured:
    """The headers of the last response a call received, for one interceptor.

    ``relayed`` is whether a relaying transport carried the call at all;
    ``headers`` holds lower-cased names, empty until a response arrives.
    """

    def __init__(self) -> None:
        self.relayed = False
        self.headers: dict[str, str] = {}

    def get(self, name: str) -> str | None:
        return self.headers.get(name.lower())

    def warn_unless_relayed(self) -> None:
        """Warn, once per process by the default filter, that this call's
        HTTP client does not relay headers."""
        if not self.relayed:
            warnings.warn(_NOT_RELAYED, RuntimeWarning, stacklevel=2)


# Every interceptor waiting on the current call, outermost first: an error
# interceptor and a retry interceptor nest, and both need the headers.
_waiting: ContextVar[tuple[Captured, ...]] = ContextVar("paladin_response_headers", default=())


@contextmanager
def captured() -> Iterator[Captured]:
    """Receive the headers of the responses to calls made within."""
    holder = Captured()
    token = _waiting.set((*_waiting.get(), holder))
    try:
        yield holder
    finally:
        _waiting.reset(token)


def _sent() -> None:
    for holder in _waiting.get():
        holder.relayed = True


def _received(headers: Mapping[str, str]) -> None:
    holders = _waiting.get()
    if not holders:
        return
    values = {name.lower(): value for name, value in headers.items()}
    for holder in holders:
        holder.headers = values


class RelaySyncTransport:
    """A ``pyqwest.SyncTransport`` that relays response headers to the SDK;
    ``inner`` defaults to pyqwest's shared transport."""

    def __init__(self, inner: Any = None) -> None:
        self._inner = inner if inner is not None else pyqwest.get_default_sync_transport()

    def execute_sync(self, request: Any) -> Any:
        _sent()
        response = self._inner.execute_sync(request)
        _received(response.headers)
        return response


class RelayTransport:
    """``RelaySyncTransport`` for the async clients."""

    def __init__(self, inner: Any = None) -> None:
        self._inner = inner if inner is not None else pyqwest.get_default_transport()

    async def execute(self, request: Any) -> Any:
        _sent()
        response = await self._inner.execute(request)
        _received(response.headers)
        return response
