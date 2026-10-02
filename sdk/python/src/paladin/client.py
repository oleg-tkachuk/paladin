"""A thin client over the generated Connect stubs.

It owns what every call needs and nothing else: the base URL of one plane,
credentials, the idempotency key, and retries for calls that are safe to
repeat. The service clients are the generated ones::

    client = Client("https://admin.example.com", bearer_token=token)
    tenants = TenantServiceClientSync(client.base_url, interceptors=client.interceptors())
"""

from __future__ import annotations

import asyncio
import random
import secrets
import time
from collections.abc import Awaitable, Callable, Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from importlib import metadata
from typing import TypeVar
from urllib.parse import urlsplit

from connectrpc.client import ResponseMetadata
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
HEADER_USER_AGENT = "User-Agent"
HEADER_RETRY_AFTER = "Retry-After"

# The distribution this module ships in, and how it names itself to the server.
_DISTRIBUTION = "paladin-sdk"
_USER_AGENT_PRODUCT = "paladin-sdk-python"
# Reported when the SDK runs from a source tree rather than an installed package.
_DEVEL_VERSION = "devel"
# Bytes of randomness in a minted idempotency key.
_IDEMPOTENCY_KEY_BYTES = 16
_MS_PER_SECOND = 1000.0

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

    Without one, a call the contract does not declare free of side effects or
    idempotent gets a fresh key of its own — or the request's
    ``idempotency_key`` field, when set — and keeps it across retries. The
    server requires a key on ``Create*`` and ``Issue*`` calls.
    """
    token = _idempotency_key.set(key or None)
    try:
        yield
    finally:
        _idempotency_key.reset(token)


def current_idempotency_key() -> str | None:
    """The key ``idempotency_key`` set for the current context, if any."""
    return _idempotency_key.get()


def user_agent() -> str:
    """``paladin-sdk-python/<version>``, the version being the API contract's."""
    try:
        version = metadata.version(_DISTRIBUTION)
    except metadata.PackageNotFoundError:
        version = _DEVEL_VERSION
    return f"{_USER_AGENT_PRODUCT}/{version}"


@dataclass(frozen=True)
class Retry:
    """Retry unary calls that failed transiently and are safe to repeat.

    ``attempts`` counts calls in total, the first one included. A call is safe
    to repeat when the contract declares it free of side effects or
    idempotent, or when it carries an idempotency key — which every call with
    side effects does. The wait before a retry is drawn at random up to a
    ceiling that doubles from ``base_delay`` to ``max_delay``, and is never
    shorter than a ``Retry-After`` the server sent; a retry that could not
    start before the call's timeout is not made. Streams are never retried.

    Each attempt runs inside its own ``connectrpc.client.ResponseMetadata``
    to read that header, so a ``ResponseMetadata`` around a retried call sees
    nothing.
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
        """The ceiling of the pause before each retry, in seconds."""
        delay = self.base_delay
        for _ in range(self.attempts - 1):
            yield delay
            delay = min(delay * 2, self.max_delay)

    def wait(self, ceiling: float, retry_after: float | None) -> float:
        """A random share of ``ceiling``, at least the server's Retry-After."""
        wait = random.uniform(0, ceiling)  # jitter, not a secret
        return max(wait, retry_after) if retry_after is not None else wait


class Client:
    """What the generated service clients of one plane need."""

    def __init__(
        self,
        base_url: str,
        *,
        bearer_token: str | None = None,
        api_token: str | None = None,
        capability: str | None = None,
        retry: Retry | None = None,
        headers: dict[str, str] | None = None,
    ) -> None:
        """Validate ``base_url`` and hold the credentials and retry policy.

        ``bearer_token`` is sent as ``Authorization: Bearer <token>``; an API
        token (``paladin_pat_…``) and an OIDC JWT are both accepted there.
        ``api_token`` is sent in ``X-Paladin-API-Token``, for a proxy that
        strips ``Authorization``. ``capability`` is sent in
        ``X-Paladin-Capability``. ``headers`` are sent on every call and
        replace what the SDK would send there, the User-Agent included.
        """
        if not base_url or not base_url.strip():
            raise ValueError("base URL is empty")
        parts = urlsplit(base_url)
        if parts.scheme not in _URL_SCHEMES or not parts.netloc:
            raise ValueError(f"base URL must be an absolute http or https URL: {base_url!r}")
        self._base_url = base_url.rstrip("/")
        sent: dict[str, str] = {HEADER_USER_AGENT: user_agent()}
        if bearer_token:
            sent[HEADER_AUTHORIZATION] = f"{_BEARER_SCHEME} {bearer_token}"
        if api_token:
            sent[HEADER_API_TOKEN] = api_token
        if capability:
            sent[HEADER_CAPABILITY] = capability
        sent.update(headers or {})
        self._headers = sent
        self._retry = retry

    @property
    def base_url(self) -> str:
        """The first argument of every generated service client."""
        return self._base_url

    def interceptors(self) -> list[InterceptorSync]:
        """Interceptors for a generated ``…ClientSync``."""
        result: list[InterceptorSync] = [_HeadersSync(self._headers), _IdempotencySync()]
        if self._retry is not None:
            result.append(_RetrySync(self._retry))
        return result

    def async_interceptors(self) -> list[Interceptor]:
        """Interceptors for a generated async ``…Client``."""
        result: list[Interceptor] = [_HeadersAsync(self._headers), _IdempotencyAsync()]
        if self._retry is not None:
            result.append(_RetryAsync(self._retry))
        return result


def _apply_headers(headers: dict[str, str], ctx: RequestContext) -> None:
    for name, value in headers.items():
        ctx.request_headers()[name] = value


def _key_for(request: object, ctx: RequestContext) -> str | None:
    """The key a unary call sends: the block's, else the request's own field,
    else a fresh one when the call has side effects the contract makes no
    promise about. None when the call needs none."""
    key = current_idempotency_key()
    if key:
        return key
    if ctx.method().idempotency_level != IdempotencyLevel.UNKNOWN:
        return None
    body = getattr(request, "idempotency_key", "")
    if isinstance(body, str) and body:
        return body
    return secrets.token_hex(_IDEMPOTENCY_KEY_BYTES)


def _stamp(request: object, ctx: RequestContext) -> None:
    key = _key_for(request, ctx)
    if key:
        ctx.request_headers()[HEADER_IDEMPOTENCY_KEY] = key


def _retry_after(meta: ResponseMetadata) -> float | None:
    """A Retry-After the server sent, in seconds."""
    value = meta.headers().get(HEADER_RETRY_AFTER)
    if value is None:
        return None
    try:
        seconds = int(value)
    except ValueError:
        return None
    return float(seconds) if seconds >= 0 else None


def _retryable(err: ConnectError, ctx: RequestContext) -> bool:
    if err.code not in _TRANSIENT_CODES:
        return False
    if ctx.method().idempotency_level != IdempotencyLevel.UNKNOWN:
        return True
    return bool(ctx.request_headers().get(HEADER_IDEMPOTENCY_KEY))


def _fits(wait: float, ctx: RequestContext) -> bool:
    """Whether a retry after ``wait`` seconds starts before the call's timeout."""
    remaining = ctx.timeout_ms()
    return remaining is None or wait * _MS_PER_SECOND < remaining


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


class _IdempotencySync:
    """Stamps the idempotency key before the retry interceptor, so every
    attempt of one call carries the same key."""

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        _stamp(request, ctx)
        return call_next(request, ctx)


class _IdempotencyAsync:
    async def intercept_unary(
        self,
        call_next: Callable[[REQ, RequestContext], Awaitable[RES]],
        request: REQ,
        ctx: RequestContext,
    ) -> RES:
        _stamp(request, ctx)
        return await call_next(request, ctx)


class _RetrySync:
    def __init__(self, retry: Retry) -> None:
        self._retry = retry

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        for ceiling in self._retry.delays():
            try:
                with ResponseMetadata() as meta:
                    return call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(err, ctx):
                    raise
                wait = self._retry.wait(ceiling, _retry_after(meta))
                if not _fits(wait, ctx):
                    raise
            time.sleep(wait)
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
        for ceiling in self._retry.delays():
            try:
                with ResponseMetadata() as meta:
                    return await call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(err, ctx):
                    raise
                wait = self._retry.wait(ceiling, _retry_after(meta))
                if not _fits(wait, ctx):
                    raise
            await asyncio.sleep(wait)
        return await call_next(request, ctx)
