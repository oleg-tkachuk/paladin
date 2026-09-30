"""A thin client over the generated Connect stubs.

It owns what every call needs and nothing else: the base URL of one plane,
credentials, the idempotency key, and retries for calls that are safe to
repeat. The service clients are the generated ones::

    client = Client("https://admin.example.com", bearer_token=token)
    tenants = TenantServiceClientSync(client.base_url, interceptors=client.interceptors())
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from typing import TypeVar
from urllib.parse import urlsplit

from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.interceptor import Interceptor, InterceptorSync
from connectrpc.method import IdempotencyLevel
from connectrpc.request import RequestContext

REQ = TypeVar("REQ")
RES = TypeVar("RES")

# Header names the server reads. They match the constants in the Go SDK, which
# the server imports; tests/test_headers.py fails when the two part.
HEADER_AUTHORIZATION = "Authorization"
HEADER_API_TOKEN = "X-Paladin-API-Token"
HEADER_CAPABILITY = "X-Paladin-Capability"
HEADER_IDEMPOTENCY_KEY = "Idempotency-Key"

_BEARER_SCHEME = "Bearer"
_URL_SCHEMES = ("http", "https")
_TRANSIENT_CODES = (Code.UNAVAILABLE, Code.RESOURCE_EXHAUSTED)

DEFAULT_RETRY_BASE_DELAY = 0.1
"""Seconds before the first retry."""
DEFAULT_RETRY_MAX_DELAY = 5.0
"""Seconds the doubling delay stops at."""

_idempotency_key: ContextVar[str | None] = ContextVar("paladin_idempotency_key", default=None)


@contextmanager
def idempotency_key(key: str) -> Iterator[None]:
    """Send ``key`` as the idempotency key on every call made inside the block.

    The server replays the first response for a key it has already seen, so a
    mutating call repeated with the same key is safe. Reuse a key only for the
    same logical operation.
    """
    token = _idempotency_key.set(key or None)
    try:
        yield
    finally:
        _idempotency_key.reset(token)


def current_idempotency_key() -> str | None:
    """The key ``idempotency_key`` set for the current context, if any."""
    return _idempotency_key.get()


@dataclass(frozen=True)
class Retry:
    """Retry unary calls that failed transiently and are safe to repeat.

    ``attempts`` counts calls in total, the first one included. A call is safe
    to repeat when the contract declares it free of side effects or
    idempotent, or when it carries an idempotency key. Streams are never
    retried.
    """

    attempts: int
    base_delay: float = DEFAULT_RETRY_BASE_DELAY
    max_delay: float = DEFAULT_RETRY_MAX_DELAY

    def __post_init__(self) -> None:
        if self.attempts < 1:
            raise ValueError("Retry.attempts must be at least 1")
        if self.base_delay <= 0 or self.max_delay < self.base_delay:
            raise ValueError("Retry delays must satisfy 0 < base_delay <= max_delay")

    def delays(self) -> Iterator[float]:
        """The pause before each retry, in seconds."""
        delay = self.base_delay
        for _ in range(self.attempts - 1):
            yield delay
            delay = min(delay * 2, self.max_delay)


class Client:
    """What the generated service clients of one plane need."""

    def __init__(
        self,
        base_url: str,
        *,
        bearer_token: str | None = None,
        capability: str | None = None,
        retry: Retry | None = None,
    ) -> None:
        """Validate ``base_url`` and hold the credentials and retry policy.

        ``bearer_token`` is sent as ``Authorization: Bearer <token>``; an API
        token (``paladin_pat_…``) and an OIDC JWT are both accepted there.
        ``capability`` is sent in ``X-Paladin-Capability``.
        """
        if not base_url or not base_url.strip():
            raise ValueError("base URL is empty")
        parts = urlsplit(base_url)
        if parts.scheme not in _URL_SCHEMES or not parts.netloc:
            raise ValueError(f"base URL must be an absolute http or https URL: {base_url!r}")
        self._base_url = base_url.rstrip("/")
        headers: dict[str, str] = {}
        if bearer_token:
            headers[HEADER_AUTHORIZATION] = f"{_BEARER_SCHEME} {bearer_token}"
        if capability:
            headers[HEADER_CAPABILITY] = capability
        self._headers = headers
        self._retry = retry

    @property
    def base_url(self) -> str:
        """The first argument of every generated service client."""
        return self._base_url

    def interceptors(self) -> list[InterceptorSync]:
        """Interceptors for a generated ``…ClientSync``."""
        result: list[InterceptorSync] = [_HeadersSync(self._headers)]
        if self._retry is not None:
            result.append(_RetrySync(self._retry))
        return result

    def async_interceptors(self) -> list[Interceptor]:
        """Interceptors for a generated async ``…Client``."""
        result: list[Interceptor] = [_HeadersAsync(self._headers)]
        if self._retry is not None:
            result.append(_RetryAsync(self._retry))
        return result


def _apply_headers(headers: dict[str, str], ctx: RequestContext) -> None:
    for name, value in headers.items():
        ctx.request_headers()[name] = value
    key = current_idempotency_key()
    if key:
        ctx.request_headers()[HEADER_IDEMPOTENCY_KEY] = key


def _retryable(err: ConnectError, ctx: RequestContext) -> bool:
    if err.code not in _TRANSIENT_CODES:
        return False
    if ctx.method().idempotency_level != IdempotencyLevel.UNKNOWN:
        return True
    return current_idempotency_key() is not None


class _HeadersSync:
    def __init__(self, headers: dict[str, str]) -> None:
        self._headers = headers

    def on_start_sync(self, ctx: RequestContext) -> None:
        _apply_headers(self._headers, ctx)

    def on_end_sync(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


class _HeadersAsync:
    def __init__(self, headers: dict[str, str]) -> None:
        self._headers = headers

    async def on_start(self, ctx: RequestContext) -> None:
        _apply_headers(self._headers, ctx)

    async def on_end(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


class _RetrySync:
    def __init__(self, retry: Retry) -> None:
        self._retry = retry

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        for delay in self._retry.delays():
            try:
                return call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(err, ctx):
                    raise
            time.sleep(delay)
        return call_next(request, ctx)


class _RetryAsync:
    def __init__(self, retry: Retry) -> None:
        self._retry = retry

    async def intercept_unary(
        self,
        call_next: Callable[[REQ, RequestContext], Awaitable[RES]],
        request: REQ,
        ctx: RequestContext,
    ) -> RES:
        for delay in self._retry.delays():
            try:
                return await call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(err, ctx):
                    raise
            await asyncio.sleep(delay)
        return await call_next(request, ctx)
