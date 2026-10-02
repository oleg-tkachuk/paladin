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
from typing import Protocol

from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin.client import Client
from paladin.iam.v1 import auth_service_pb2, types_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceClient, AuthServiceClientSync

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
    """A ``TokenSource`` for a user who signs in. Safe to share between threads."""

    def __init__(self, auth: AuthServiceClientSync, state: _SessionState) -> None:
        self._auth = auth
        self._state = state
        self._lock = threading.Lock()

    @classmethod
    def sign_in(
        cls,
        iam_url: str,
        subject: str,
        password: str,
        *,
        clock: Callable[[], float] = time.monotonic,
    ) -> Session:
        """Sign in at the IAM plane. The password is kept so the session can
        sign in again once its refresh token expires."""
        session = cls(
            _auth_client(iam_url), _SessionState(_login_request(subject, password), "", clock)
        )
        with session._lock:
            session._sign_in()
        return session

    @classmethod
    def from_refresh_token(
        cls, iam_url: str, refresh_token: str, *, clock: Callable[[], float] = time.monotonic
    ) -> Session:
        """Resume from a refresh token issued for the IAM plane. It cannot sign
        in again once that token expires."""
        if not refresh_token:
            raise ValueError("from_refresh_token needs a refresh token")
        return cls(_auth_client(iam_url), _SessionState(None, refresh_token, clock))

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
            try:
                self._mint(audience)
            except ConnectError as err:
                if not _needs_sign_in(err, self._state):
                    raise
                self._sign_in()
                self._mint(audience)
            return self._state.cached[audience].token

    def invalidate(self, audience: str) -> None:
        """Drop the cached token for ``audience``; the next ``token`` mints one."""
        with self._lock:
            self._state.cached.pop(audience, None)

    def _sign_in(self) -> None:
        assert self._state.login is not None
        self._state.keep_pair(self._auth.login(self._state.login).tokens)

    def _mint(self, audience: str) -> None:
        if audience == AUDIENCE_IAM:
            self._state.keep_pair(self._auth.refresh_token(self._state.refresh_request()).tokens)
            return
        resp = self._auth.exchange_audience(self._state.exchange_request(audience))
        self._state.keep(audience, resp.access_token, resp.access_expires_in_seconds)


class AsyncSession:
    """``Session`` for asyncio: safe to share between tasks of one loop."""

    def __init__(self, auth: AuthServiceClient, state: _SessionState) -> None:
        self._auth = auth
        self._state = state
        self._lock = asyncio.Lock()

    @classmethod
    async def sign_in(
        cls,
        iam_url: str,
        subject: str,
        password: str,
        *,
        clock: Callable[[], float] = time.monotonic,
    ) -> AsyncSession:
        session = cls(
            _async_auth_client(iam_url), _SessionState(_login_request(subject, password), "", clock)
        )
        async with session._lock:
            await session._sign_in()
        return session

    @classmethod
    def from_refresh_token(
        cls, iam_url: str, refresh_token: str, *, clock: Callable[[], float] = time.monotonic
    ) -> AsyncSession:
        if not refresh_token:
            raise ValueError("from_refresh_token needs a refresh token")
        return cls(_async_auth_client(iam_url), _SessionState(None, refresh_token, clock))

    @property
    def refresh_token(self) -> str:
        return self._state.refresh

    async def token(self, audience: str) -> str:
        async with self._lock:
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


def _auth_client(iam_url: str) -> AuthServiceClientSync:
    client = Client(iam_url)
    return AuthServiceClientSync(client.base_url, interceptors=client.interceptors())


def _async_auth_client(iam_url: str) -> AuthServiceClient:
    client = Client(iam_url)
    return AuthServiceClient(client.base_url, interceptors=client.async_interceptors())
