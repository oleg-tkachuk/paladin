"""A thin client over the generated Connect stubs.

It owns what every call needs and nothing else: the base URL of one plane,
credentials, the idempotency key, and retries for calls that are safe to
repeat. The service clients are the generated ones::

    client = Client("https://admin.example.com", bearer_token=token)
    tenants = TenantServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    )
"""

from __future__ import annotations

import asyncio
import functools
import json
import random
import re
import secrets
import time
from collections.abc import Awaitable, Callable, Iterator, Sequence
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from importlib import metadata
from typing import Any, TypeVar
from urllib.parse import urlsplit

import pyqwest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.interceptor import Interceptor, InterceptorSync
from connectrpc.method import IdempotencyLevel
from connectrpc.request import RequestContext

from paladin._tls_http import call_deadline
from paladin.dpop import DPoPAsync, DPoPSync
from paladin.errors import convert
from paladin.observe import Hooks, RetryEvent, report_retry
from paladin.relay import Captured, RelaySyncTransport, RelayTransport, captured

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
# The local label hatch-vcs records when it could not learn the version: the
# end of fallback_version in pyproject.toml, which tests/test_version.py
# checks against this.
UNKNOWN_VERSION_LABEL = "+unknown"
# An SDK release tag, as tag_regex in pyproject.toml reads it; the test checks
# the two are the same pattern.
RELEASE_TAG = re.compile(r"^sdk/go/v(?P<version>[0-9]+\.[0-9]+\.[0-9]+)$")
_TAG_VERSION_GROUP = "version"
# PEP 610: where an installer records the URL and revision it installed from.
_DIRECT_URL = "direct_url.json"
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
# Set by no_idempotency_key: calls go out with no key at all.
_NO_KEY = ""


@contextmanager
def idempotency_key(key: str) -> Iterator[None]:
    """Send ``key`` as the idempotency key on the calls made inside the block
    that the contract does not declare free of side effects or idempotent —
    those never carry one.

    The server replays the first response to a request it has already seen
    with that key, and refuses the key reused for a different request to the
    same method, so a mutating call repeated with the same key is safe. Reuse
    a key only for the same logical operation. ``upload``, ``download`` and
    their ``_many`` forms keep it for the calls that create or complete an
    object and give every other call its own.

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


@contextmanager
def no_idempotency_key() -> Iterator[None]:
    """Send every call made inside the block without an idempotency key: not
    the default fresh one, not the request's own field. Such a call is never
    retried, unless the contract declares it free of side effects or
    idempotent — for an operation that must run again when repeated rather
    than be answered with the first response. The server refuses a
    ``Create*`` or ``Issue*`` call without a key. The innermost block wins."""
    token = _idempotency_key.set(_NO_KEY)
    try:
        yield
    finally:
        _idempotency_key.reset(token)


@contextmanager
def _own_keys() -> Iterator[None]:
    """Lift any ``idempotency_key`` or ``no_idempotency_key`` for the block,
    so each call in it gets the default: a fresh key of its own. For the calls
    a workflow makes more than once per operation — ``download_object`` on a
    retry after a URL expired, ``presign_part`` per part — which one shared key
    would make the server refuse."""
    token = _idempotency_key.set(None)
    try:
        yield
    finally:
        _idempotency_key.reset(token)


def current_idempotency_key() -> str | None:
    """The key ``idempotency_key`` set for the current context, if any."""
    return _idempotency_key.get() or None


@functools.cache
def sdk_version() -> str:
    """This package's installed version; ``devel`` for a source tree.

    A build with neither git nor a source archive — Poetry installs a git
    dependency through Dulwich, with no git for hatch-vcs to ask — records
    ``0.0.0+unknown``. The installer still records the tag it was asked for,
    in the distribution's ``direct_url.json`` (PEP 610), and the version is
    read from there.
    """
    try:
        dist = metadata.distribution(_DISTRIBUTION)
    except metadata.PackageNotFoundError:
        return _DEVEL_VERSION
    if not dist.version.endswith(UNKNOWN_VERSION_LABEL):
        return dist.version
    return _version_from_direct_url(dist.read_text(_DIRECT_URL)) or dist.version


def _version_from_direct_url(text: str | None) -> str | None:
    """The release a ``direct_url.json`` names by its tag; None when it names
    none — no file, not a VCS install, or a branch or commit."""
    if not text:
        return None
    try:
        revision = json.loads(text).get("vcs_info", {}).get("requested_revision", "")
    except (ValueError, AttributeError):
        return None
    found = RELEASE_TAG.fullmatch(revision or "")
    return found.group(_TAG_VERSION_GROUP) if found else None


def user_agent() -> str:
    """``paladin-sdk-python/<version>``, the version being the API contract's."""
    return f"{_USER_AGENT_PRODUCT}/{sdk_version()}"


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

    A ``connectrpc.client.ResponseMetadata`` around a retried call sees the
    last attempt's headers.
    """

    attempts: int
    base_delay: float = DEFAULT_RETRY_BASE_DELAY
    max_delay: float = DEFAULT_RETRY_MAX_DELAY
    retryable: Callable[[ConnectError], bool] | None = None
    """Which failures are transient; None is ``default_retryable``. Only that:
    a call is still retried only when it is safe to repeat."""

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
        capability_source: Callable[[], Any] | None = None,
        retry: Retry | None = None,
        headers: dict[str, str] | None = None,
        token_source: Any = None,
        audience: str | None = None,
        user_agent_suffix: str | None = None,
        hooks: Hooks | None = None,
        interceptors: Sequence[Any] = (),
        dpop_key: Any = None,
    ) -> None:
        """Validate ``base_url`` and hold the credentials and retry policy.

        ``bearer_token`` is sent as ``Authorization: Bearer <token>``; an API
        token (``paladin_pat_…``) and an OIDC JWT are both accepted there.
        ``api_token`` is sent in ``X-Paladin-API-Token``, for a proxy that
        strips ``Authorization``. ``capability`` is sent in
        ``X-Paladin-Capability``. ``capability_source`` is called for each
        call — an async client awaits what it returns — and its token sent
        there instead, for a client that acts for many callers: read the
        caller from a ``contextvars.ContextVar``. One that returns nothing
        leaves ``capability``'s, if any. ``headers`` are sent on every call and
        replace what the SDK would send there, the User-Agent included.

        ``token_source`` — a ``StaticToken``, ``Session`` or ``AsyncSession``
        — supplies the bearer token for ``audience``, this plane's; a call the
        server refuses as unauthenticated is made once more with a fresh one.
        ``connect`` sets ``audience`` for each plane.

        ``user_agent_suffix`` — ``"worker/2.1"`` — is appended to the SDK's
        User-Agent. ``hooks`` are told of every retry. ``interceptors`` run
        outside the SDK's own, so one that times or traces a call covers its
        retries; give sync or async ones to match the clients. (connectrpc-otel
        0.2.0 is not one to use here: it fails on connect-python 0.9.0 — see
        the README's OpenTelemetry section.)

        ``dpop_key`` — a ``cryptography`` Ed25519 or P-256 private key — proves
        possession of the key a ``capability`` is bound to: each call, each
        retry included, carries a fresh DPoP proof (RFC 9449). Needs the
        ``dpop`` extra; see ``paladin.dpop``.
        """
        if token_source is not None and not audience:
            raise ValueError("token_source needs the plane's audience; or use paladin.connect")
        if not base_url or not base_url.strip():
            raise ValueError("base URL is empty")
        parts = urlsplit(base_url)
        if parts.scheme not in _URL_SCHEMES or not parts.netloc:
            raise ValueError(f"base URL must be an absolute http or https URL: {base_url!r}")
        self._base_url = base_url.rstrip("/")
        agent = f"{user_agent()} {user_agent_suffix}" if user_agent_suffix else user_agent()
        sent: dict[str, str] = {HEADER_USER_AGENT: agent}
        if bearer_token:
            sent[HEADER_AUTHORIZATION] = f"{_BEARER_SCHEME} {bearer_token}"
        if api_token:
            sent[HEADER_API_TOKEN] = api_token
        if capability:
            sent[HEADER_CAPABILITY] = capability
        sent.update(headers or {})
        self._headers = sent
        self._retry = retry
        self._token_source = token_source
        self._capability_source = capability_source
        self._audience = audience or ""
        self._hooks = hooks
        self._extra = list(interceptors)
        self._dpop: tuple[DPoPSync, DPoPAsync] | None = None
        if dpop_key is not None:
            self._dpop = (
                DPoPSync(dpop_key, self._base_url, HEADER_CAPABILITY),
                DPoPAsync(dpop_key, self._base_url, HEADER_CAPABILITY),
            )

    @property
    def base_url(self) -> str:
        """The first argument of every generated service client."""
        return self._base_url

    def http_client(self, transport: Any = None) -> pyqwest.SyncClient:
        """The ``http_client`` for a generated ``…ClientSync``: it relays the
        response headers that typed errors and retries read. ``transport`` is
        a ``pyqwest.SyncTransport`` to send through, pyqwest's shared one by
        default."""
        return pyqwest.SyncClient(RelaySyncTransport(transport))

    def async_http_client(self, transport: Any = None) -> pyqwest.Client:
        """``http_client`` for a generated async ``…Client``."""
        return pyqwest.Client(RelayTransport(transport))

    def interceptors(self) -> list[InterceptorSync]:
        """Interceptors for a generated ``…ClientSync``."""
        result: list[InterceptorSync] = [
            *self._extra,
            _ErrorsSync(),
            _HeadersSync(self._headers),
            _IdempotencySync(),
        ]
        if self._capability_source is not None:
            # Before DPoP, which signs over the capability it sets.
            result.append(_CapabilitySync(self._capability_source))
        if self._token_source is not None:
            result.append(_TokensSync(self._token_source, self._audience))
        if self._retry is not None:
            result.append(_RetrySync(self._retry, self._hooks))
        if self._dpop is not None:
            result.append(self._dpop[0])
        # Innermost, so each attempt of a retried call gets its own.
        result.append(_DeadlineSync())
        return result

    def async_interceptors(self) -> list[Interceptor]:
        """Interceptors for a generated async ``…Client``."""
        result: list[Interceptor] = [
            *self._extra,
            _ErrorsAsync(),
            _HeadersAsync(self._headers),
            _IdempotencyAsync(),
        ]
        if self._capability_source is not None:
            result.append(_CapabilityAsync(self._capability_source))
        if self._token_source is not None:
            result.append(_TokensAsync(self._token_source, self._audience))
        if self._retry is not None:
            result.append(_RetryAsync(self._retry, self._hooks))
        if self._dpop is not None:
            result.append(self._dpop[1])
        return result


class _CapabilitySync:
    """Sends the token ``capability_source`` returns for each call."""

    def __init__(self, source: Callable[[], Any]) -> None:
        self._source = source

    def on_start_sync(self, ctx: RequestContext) -> None:
        token = self._source()
        if token:
            ctx.request_headers()[HEADER_CAPABILITY] = token

    def on_end_sync(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


class _CapabilityAsync:
    def __init__(self, source: Callable[[], Any]) -> None:
        self._source = source

    async def on_start(self, ctx: RequestContext) -> None:
        token = self._source()
        if asyncio.iscoroutine(token):
            token = await token
        if token:
            ctx.request_headers()[HEADER_CAPABILITY] = token

    async def on_end(self, token: None, ctx: RequestContext, error: Exception | None) -> None:
        return None


def _apply_headers(headers: dict[str, str], ctx: RequestContext) -> None:
    for name, value in headers.items():
        ctx.request_headers()[name] = value


def _key_for(request: object, ctx: RequestContext) -> str | None:
    """The key a unary call sends: the block's, else the request's own field,
    else a fresh one when the call has side effects the contract makes no
    promise about. None when the call needs none."""
    chosen = _idempotency_key.get()
    if chosen == _NO_KEY:
        return None
    # A call declared free of side effects or idempotent never carries one,
    # not even the block's: the server does not memoise a read, and an
    # idempotent call — regenerate_upload_url — must run again when repeated
    # rather than hand back the URL it is replacing.
    if ctx.method().idempotency_level != IdempotencyLevel.UNKNOWN:
        return None
    if chosen:
        return chosen
    body = getattr(request, "idempotency_key", "")
    if isinstance(body, str) and body:
        return body
    return secrets.token_hex(_IDEMPOTENCY_KEY_BYTES)


def _stamp(request: object, ctx: RequestContext) -> None:
    key = _key_for(request, ctx)
    if key:
        ctx.request_headers()[HEADER_IDEMPOTENCY_KEY] = key


def _retry_after(meta: Captured) -> float | None:
    """A Retry-After the server sent, in seconds."""
    value = meta.get(HEADER_RETRY_AFTER)
    return None if value is None else parse_retry_after(value)


def parse_retry_after(value: str, now: datetime | None = None) -> float | None:
    """Seconds to wait for a Retry-After value: a number of seconds, or an HTTP
    date (RFC 9110), which waits until then. None for anything else."""
    try:
        seconds = int(value)
    except ValueError:
        pass
    else:
        return float(seconds) if seconds >= 0 else None
    try:
        at = parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None
    if at.tzinfo is None:
        return None
    return max((at - (now or datetime.now(timezone.utc))).total_seconds(), 0.0)


def default_retryable(err: ConnectError) -> bool:
    """The failures ``Retry`` retries unless given ``retryable``: Unavailable
    and ResourceExhausted, the two a server sends for a condition that passes."""
    return err.code in _TRANSIENT_CODES


def _retryable(retry: Retry, err: ConnectError, ctx: RequestContext) -> bool:
    """Whether err is transient and the call safe to repeat: declared free of
    side effects or idempotent, or carrying an idempotency key. The second
    half is not the classifier's to decide."""
    if not (retry.retryable or default_retryable)(err):
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


def _bearer(token: str) -> str:
    return f"{_BEARER_SCHEME} {token}"


class _TokensSync:
    """Sends the source's token, and makes a call the server refused as
    unauthenticated once more with a fresh one: the server authenticates
    before it does anything else, so the first attempt changed nothing."""

    def __init__(self, source: Any, audience: str) -> None:
        self._source = source
        self._audience = audience

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        ctx.request_headers()[HEADER_AUTHORIZATION] = _bearer(self._source.token(self._audience))
        try:
            return call_next(request, ctx)
        except ConnectError as err:
            invalidate = getattr(self._source, "invalidate", None)
            if err.code != Code.UNAUTHENTICATED or invalidate is None:
                raise
            invalidate(self._audience)
        ctx.request_headers()[HEADER_AUTHORIZATION] = _bearer(self._source.token(self._audience))
        return call_next(request, ctx)


class _TokensAsync:
    def __init__(self, source: Any, audience: str) -> None:
        self._source = source
        self._audience = audience

    async def _token(self) -> str:
        token = self._source.token(self._audience)
        return await token if asyncio.iscoroutine(token) else token

    async def intercept_unary(
        self,
        call_next: Callable[[REQ, RequestContext], Awaitable[RES]],
        request: REQ,
        ctx: RequestContext,
    ) -> RES:
        ctx.request_headers()[HEADER_AUTHORIZATION] = _bearer(await self._token())
        try:
            return await call_next(request, ctx)
        except ConnectError as err:
            invalidate = getattr(self._source, "invalidate", None)
            if err.code != Code.UNAUTHENTICATED or invalidate is None:
                raise
            invalidate(self._audience)
        ctx.request_headers()[HEADER_AUTHORIZATION] = _bearer(await self._token())
        return await call_next(request, ctx)


class _RetrySync:
    def __init__(self, retry: Retry, hooks: Hooks | None) -> None:
        self._retry = retry
        self._hooks = hooks

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        attempt = 0
        for ceiling in self._retry.delays():
            try:
                with captured() as meta:
                    return call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(self._retry, err, ctx):
                    raise
                wait = self._retry.wait(ceiling, _retry_after(meta))
                if not _fits(wait, ctx):
                    raise
                attempt += 1
                report_retry(self._hooks, RetryEvent(_procedure(ctx), attempt, wait, err))
            time.sleep(wait)
        return call_next(request, ctx)


class _RetryAsync:
    def __init__(self, retry: Retry, hooks: Hooks | None) -> None:
        self._retry = retry
        self._hooks = hooks

    async def intercept_unary(
        self,
        call_next: Callable[[REQ, RequestContext], Awaitable[RES]],
        request: REQ,
        ctx: RequestContext,
    ) -> RES:
        attempt = 0
        for ceiling in self._retry.delays():
            try:
                with captured() as meta:
                    return await call_next(request, ctx)
            except ConnectError as err:
                if not _retryable(self._retry, err, ctx):
                    raise
                wait = self._retry.wait(ceiling, _retry_after(meta))
                if not _fits(wait, ctx):
                    raise
                attempt += 1
                report_retry(self._hooks, RetryEvent(_procedure(ctx), attempt, wait, err))
            await asyncio.sleep(wait)
        return await call_next(request, ctx)


def _procedure(ctx: RequestContext) -> str:
    method = ctx.method()
    return f"/{method.service_name}/{method.name}"


def _typed(err: ConnectError, ctx: RequestContext, meta: Captured) -> ConnectError:
    return convert(err, _procedure(ctx), meta, sdk_version(), parse_retry_after)


class _DeadlineSync:
    """Hands the attempt's deadline to the SDK's TLS transport, which pyqwest
    tells it only through a private module; an async call is cancelled by
    connect-python instead."""

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        timeout_ms = ctx.timeout_ms()
        if timeout_ms is None:
            return call_next(request, ctx)
        token = call_deadline.set(time.monotonic() + timeout_ms / _MS_PER_SECOND)
        try:
            return call_next(request, ctx)
        finally:
            call_deadline.reset(token)


class _ErrorsSync:
    """Outermost: every failure reaches the caller as a ``PaladinError``,
    while the interceptors inside see the ``ConnectError`` they act on."""

    def intercept_unary_sync(
        self, call_next: Callable[[REQ, RequestContext], RES], request: REQ, ctx: RequestContext
    ) -> RES:
        with captured() as meta:
            try:
                return call_next(request, ctx)
            except ConnectError as err:
                meta.warn_unless_relayed()
                typed = _typed(err, ctx, meta)
                if typed is err:
                    raise
                raise typed from err


class _ErrorsAsync:
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
                meta.warn_unless_relayed()
                typed = _typed(err, ctx, meta)
                if typed is err:
                    raise
                raise typed from err
