"""Tokens for the planes: a fixed one, or a session that signs in.

A token is issued for one plane — its audience — and refused by the others.
A ``Session`` keeps one refresh token, issued for the IAM plane, and derives
each plane's access token from it: the IAM token by refreshing, the others by
exchange. Access tokens are cached until ``TOKEN_REFRESH_MARGIN`` seconds
before they expire.
"""

from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, Protocol

import pyqwest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin.client import Client
from paladin.iam.v1 import auth_service_pb2, types_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceClient, AuthServiceClientSync
from paladin.relay import RelaySyncTransport, RelayTransport
from paladin.tls import TLS

# The audiences of the three planes. tests/test_headers.py keeps them equal to
# the Go SDK's, which the server imports.
AUDIENCE_DATA = "paladin-data"
AUDIENCE_ADMIN = "paladin-admin"
AUDIENCE_IAM = "paladin-iam"

TOKEN_REFRESH_MARGIN = 30.0
"""Seconds before expiry at which a cached access token is replaced."""


class TokenSource(Protocol):
    """Supplies the bearer token for a call to one audience's plane."""

    def token(self, audience: str) -> str: ...


class AsyncTokenSource(Protocol):
    async def token(self, audience: str) -> str: ...


@dataclass(frozen=True)
class StaticToken:
    """The same token for every plane: an API token, or a JWT from elsewhere."""

    value: str

    def __post_init__(self) -> None:
        if not self.value:
            raise ValueError("StaticToken needs a token")

    def token(self, audience: str) -> str:
        return self.value


@dataclass
class _Cached:
    token: str
    expires: float


class _SessionState:
    """What a session knows, shared by the sync and async kinds."""

    def __init__(
        self, login: auth_service_pb2.LoginRequest | None, refresh: str, clock: Callable[[], float]
    ) -> None:
        self.login = login
        self.refresh = refresh
        self.clock = clock
        self.cached: dict[str, _Cached] = {}

    def fresh(self, audience: str) -> str | None:
        cached = self.cached.get(audience)
        if cached and self.clock() + TOKEN_REFRESH_MARGIN < cached.expires:
            return cached.token
        return None

    def keep(self, audience: str, token: str, expires_in: int) -> None:
        self.cached[audience] = _Cached(token, self.clock() + expires_in)

    def keep_pair(self, pair: types_pb2.TokenPair) -> None:
        if not pair.access_token or not pair.refresh_token:
            raise ConnectError(Code.UNAUTHENTICATED, "sign-in returned no token")
        self.refresh = pair.refresh_token
        self.keep(AUDIENCE_IAM, pair.access_token, pair.access_expires_in_seconds)

    def refresh_request(self) -> auth_service_pb2.RefreshTokenRequest:
        return auth_service_pb2.RefreshTokenRequest(
            refresh_token=self.refresh, requested_audience=AUDIENCE_IAM
        )

    def exchange_request(self, audience: str) -> auth_service_pb2.ExchangeAudienceRequest:
        return auth_service_pb2.ExchangeAudienceRequest(
            refresh_token=self.refresh, target_audience=audience
        )


def _login_request(subject: str, password: str) -> auth_service_pb2.LoginRequest:
    return auth_service_pb2.LoginRequest(
        subject=subject, password=password, requested_audience=AUDIENCE_IAM
    )


def _needs_sign_in(err: ConnectError, state: _SessionState) -> bool:
    """The refresh token itself was refused, and the session can sign in again."""
    return err.code == Code.UNAUTHENTICATED and state.login is not None


class Session:
    """A ``TokenSource`` for a user who signs in. Safe to share between threads.

    ``sign_in`` and ``from_refresh_token`` take ``connect``'s ``transport``,
    ``tls`` and ``Client`` options for the IAM plane: a CA bundle and client
    certificate for an IAM behind mTLS, a ``retry`` policy, ``timeout_ms``.
    Without them the session reaches IAM with the defaults."""

    def __init__(self, auth: AuthServiceClientSync, state: _SessionState) -> None:
        self._auth = auth
        self._state = state
        # _lock guards the state and is never held across a call to IAM, so a
        # cached token is served while another audience's is minted; _mint_lock
        # serialises those calls — a refresh rotates the refresh token, so two
        # at once would race to spend it.
        self._lock = threading.Lock()
        self._mint_lock = threading.Lock()

    @classmethod
    def sign_in(
        cls,
        iam_url: str,
        subject: str,
        password: str,
        *,
        clock: Callable[[], float] = time.monotonic,
        transport: dict[str, Any] | None = None,
        tls: TLS | None = None,
        **client_options: Any,
    ) -> Session:
        """Sign in at the IAM plane. The password is kept so the session can
        sign in again once its refresh token expires."""
        session = cls(
            _auth_client(iam_url, transport, tls, client_options),
            _SessionState(_login_request(subject, password), "", clock),
        )
        with session._mint_lock:
            session._sign_in()
        return session

    @classmethod
    def from_refresh_token(
        cls,
        iam_url: str,
        refresh_token: str,
        *,
        clock: Callable[[], float] = time.monotonic,
        transport: dict[str, Any] | None = None,
        tls: TLS | None = None,
        **client_options: Any,
    ) -> Session:
        """Resume from a refresh token issued for the IAM plane. It cannot sign
        in again once that token expires."""
        if not refresh_token:
            raise ValueError("from_refresh_token needs a refresh token")
        return cls(
            _auth_client(iam_url, transport, tls, client_options),
            _SessionState(None, refresh_token, clock),
        )

    @property
    def refresh_token(self) -> str:
        """The current refresh token, to store and resume from. Refreshing rotates it."""
        with self._lock:
            return self._state.refresh

    def token(self, audience: str) -> str:
        """A valid access token for ``audience``."""
        with self._lock:
            cached = self._state.fresh(audience)
        if cached:
            return cached
        with self._mint_lock:
            # Another thread may have minted it while this one waited.
            with self._lock:
                cached = self._state.fresh(audience)
            if cached:
                return cached
            try:
                self._mint(audience)
            except ConnectError as err:
                if not _needs_sign_in(err, self._state):
                    raise
                self._sign_in()
                self._mint(audience)
            with self._lock:
                return self._state.cached[audience].token

    def invalidate(self, audience: str) -> None:
        """Drop the cached token for ``audience``; the next ``token`` mints one."""
        with self._lock:
            self._state.cached.pop(audience, None)

    # _sign_in and _mint run with _mint_lock held, and take _lock only to
    # read or write the state, never across the call.
    def _sign_in(self) -> None:
        assert self._state.login is not None
        tokens = self._auth.login(self._state.login).tokens
        with self._lock:
            self._state.keep_pair(tokens)

    def _mint(self, audience: str) -> None:
        with self._lock:
            refresh = self._state.refresh_request()
            exchange = self._state.exchange_request(audience)
        if audience == AUDIENCE_IAM:
            tokens = self._auth.refresh_token(refresh).tokens
            with self._lock:
                self._state.keep_pair(tokens)
            return
        resp = self._auth.exchange_audience(exchange)
        with self._lock:
            self._state.keep(audience, resp.access_token, resp.access_expires_in_seconds)


class AsyncSession:
    """``Session`` for asyncio: safe to share between tasks of one loop."""

    def __init__(self, auth: AuthServiceClient, state: _SessionState) -> None:
        self._auth = auth
        self._state = state
        # Held across the call to IAM, serialising the mints. A cached token
        # is read without it, so a task whose token is cached never waits on
        # another audience's mint. One loop: the state needs no lock of its own.
        self._mint_lock = asyncio.Lock()

    @classmethod
    async def sign_in(
        cls,
        iam_url: str,
        subject: str,
        password: str,
        *,
        clock: Callable[[], float] = time.monotonic,
        transport: dict[str, Any] | None = None,
        tls: TLS | None = None,
        **client_options: Any,
    ) -> AsyncSession:
        session = cls(
            _async_auth_client(iam_url, transport, tls, client_options),
            _SessionState(_login_request(subject, password), "", clock),
        )
        async with session._mint_lock:
            await session._sign_in()
        return session

    @classmethod
    def from_refresh_token(
        cls,
        iam_url: str,
        refresh_token: str,
        *,
        clock: Callable[[], float] = time.monotonic,
        transport: dict[str, Any] | None = None,
        tls: TLS | None = None,
        **client_options: Any,
    ) -> AsyncSession:
        if not refresh_token:
            raise ValueError("from_refresh_token needs a refresh token")
        return cls(
            _async_auth_client(iam_url, transport, tls, client_options),
            _SessionState(None, refresh_token, clock),
        )

    @property
    def refresh_token(self) -> str:
        return self._state.refresh

    async def token(self, audience: str) -> str:
        cached = self._state.fresh(audience)
        if cached:
            return cached
        async with self._mint_lock:
            cached = self._state.fresh(audience)
            if cached:
                return cached
            try:
                await self._mint(audience)
            except ConnectError as err:
                if not _needs_sign_in(err, self._state):
                    raise
                await self._sign_in()
                await self._mint(audience)
            return self._state.cached[audience].token

    def invalidate(self, audience: str) -> None:
        self._state.cached.pop(audience, None)

    async def _sign_in(self) -> None:
        assert self._state.login is not None
        self._state.keep_pair((await self._auth.login(self._state.login)).tokens)

    async def _mint(self, audience: str) -> None:
        if audience == AUDIENCE_IAM:
            self._state.keep_pair(
                (await self._auth.refresh_token(self._state.refresh_request())).tokens
            )
            return
        resp = await self._auth.exchange_audience(self._state.exchange_request(audience))
        self._state.keep(audience, resp.access_token, resp.access_expires_in_seconds)


def _auth_client(
    iam_url: str, transport: dict[str, Any] | None, tls: TLS | None, options: dict[str, Any]
) -> AuthServiceClientSync:
    """The IAM client a Session signs in with, built as ``connect`` builds a
    plane's: the caller's transport and TLS, and ``Client`` options."""
    from paladin.connect import _transport_options

    extra = _transport_options(
        transport,
        tls,
        lambda t: pyqwest.SyncClient(RelaySyncTransport(t.sync_transport() if t else None)),
    )
    client = Client(iam_url, **options)
    return AuthServiceClientSync(client.base_url, interceptors=client.interceptors(), **extra)


def _async_auth_client(
    iam_url: str, transport: dict[str, Any] | None, tls: TLS | None, options: dict[str, Any]
) -> AuthServiceClient:
    """``_auth_client`` for ``AsyncSession``."""
    from paladin.connect import _transport_options

    extra = _transport_options(
        transport,
        tls,
        lambda t: pyqwest.Client(RelayTransport(t.async_transport() if t else None)),
    )
    client = Client(iam_url, **options)
    return AuthServiceClient(client.base_url, interceptors=client.async_interceptors(), **extra)
