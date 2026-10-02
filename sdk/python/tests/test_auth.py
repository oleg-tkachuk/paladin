"""Sessions, static tokens and the token interceptors, against a fake IAM."""

from __future__ import annotations

import asyncio
import io
import threading
from collections.abc import Iterator
from dataclasses import dataclass, field
from wsgiref.simple_server import WSGIRequestHandler, WSGIServer, make_server

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.request import RequestContext

from paladin import (
    AUDIENCE_ADMIN,
    AUDIENCE_DATA,
    AUDIENCE_IAM,
    HEADER_AUTHORIZATION,
    TOKEN_REFRESH_MARGIN,
    AsyncSession,
    Client,
    Session,
    StaticToken,
)
from paladin.iam.v1 import auth_service_pb2, health_service_pb2, types_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceSync, AuthServiceWSGIApplication
from paladin.iam.v1.health_service_connect import (
    HealthServiceClient,
    HealthServiceClientSync,
    HealthServiceSync,
    HealthServiceWSGIApplication,
)

# Seconds the fake IAM's access tokens live.
TOKEN_LIFETIME = 900
# Threads that ask for the same token at once.
PARALLEL_CALLERS = 16
_EPHEMERAL_PORT = 0
_LOOPBACK = "127.0.0.1"


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


@dataclass
class FakeIAM(AuthServiceSync, HealthServiceSync):
    """Issues numbered tokens and counts each kind of call."""

    logins: int = 0
    refreshes: int = 0
    exchanges: int = 0
    serial: int = 0
    valid_refresh: str = ""
    refuse_refresh: bool = False
    refuse_next_calls: int = 0
    seen_tokens: list[str] = field(default_factory=list)
    lock: threading.Lock = field(default_factory=threading.Lock)

    def _next(self, prefix: str) -> str:
        self.serial += 1
        return f"{prefix}-{self.serial}"

    def _pair(self) -> types_pb2.TokenPair:
        self.valid_refresh = self._next("refresh")
        return types_pb2.TokenPair(
            access_token=self._next("iam"),
            refresh_token=self.valid_refresh,
            access_expires_in_seconds=TOKEN_LIFETIME,
        )

    def _check(self, token: str) -> None:
        if self.refuse_refresh or token != self.valid_refresh:
            raise ConnectError(Code.UNAUTHENTICATED, "refresh token expired")

    def login(
        self, request: auth_service_pb2.LoginRequest, ctx: RequestContext
    ) -> auth_service_pb2.LoginResponse:
        with self.lock:
            self.logins += 1
            self.refuse_refresh = False
            return auth_service_pb2.LoginResponse(tokens=self._pair())

    def refresh_token(
        self, request: auth_service_pb2.RefreshTokenRequest, ctx: RequestContext
    ) -> auth_service_pb2.RefreshTokenResponse:
        with self.lock:
            self.refreshes += 1
            self._check(request.refresh_token)
            return auth_service_pb2.RefreshTokenResponse(tokens=self._pair())

    def exchange_audience(
        self, request: auth_service_pb2.ExchangeAudienceRequest, ctx: RequestContext
    ) -> auth_service_pb2.ExchangeAudienceResponse:
        with self.lock:
            self.exchanges += 1
            self._check(request.refresh_token)
            return auth_service_pb2.ExchangeAudienceResponse(
                access_token=self._next(request.target_audience),
                access_expires_in_seconds=TOKEN_LIFETIME,
            )

    def get_version(
        self, request: health_service_pb2.GetVersionRequest, ctx: RequestContext
    ) -> health_service_pb2.VersionInfo:
        with self.lock:
            self.seen_tokens.append(ctx.request_headers().get(HEADER_AUTHORIZATION, ""))
            if self.refuse_next_calls > 0:
                self.refuse_next_calls -= 1
                raise ConnectError(Code.UNAUTHENTICATED, "token revoked")
        return health_service_pb2.VersionInfo()


@dataclass
class Served:
    url: str
    iam: FakeIAM


@pytest.fixture
def iam() -> Iterator[Served]:
    fake = FakeIAM()
    apps = [AuthServiceWSGIApplication(fake), HealthServiceWSGIApplication(fake)]

    def route(environ, start_response):  # type: ignore[no-untyped-def]
        length = int(environ.get("CONTENT_LENGTH") or 0)
        environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
        for app in apps:
            if environ.get("PATH_INFO", "").startswith(app.path + "/"):
                return app(environ, start_response)
        start_response("404 Not Found", [("Content-Type", "text/plain")])
        return [b"no such service"]

    httpd: WSGIServer = make_server(_LOOPBACK, _EPHEMERAL_PORT, route, handler_class=_Quiet)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    try:
        yield Served(url=f"http://{_LOOPBACK}:{httpd.server_port}", iam=fake)
    finally:
        httpd.shutdown()
        httpd.server_close()


class Clock:
    def __init__(self) -> None:
        self.now = 0.0

    def __call__(self) -> float:
        return self.now


def test_session_caches_each_audience(iam: Served) -> None:
    s = Session.sign_in(iam.url, "admin", "secret")
    first = s.token(AUDIENCE_IAM), s.token(AUDIENCE_DATA)
    assert (s.token(AUDIENCE_IAM), s.token(AUDIENCE_DATA)) == first
    assert (iam.iam.logins, iam.iam.refreshes, iam.iam.exchanges) == (1, 0, 1)


def test_session_renews_before_expiry(iam: Served) -> None:
    clock = Clock()
    s = Session.sign_in(iam.url, "admin", "secret", clock=clock)
    iam_token, data_token, before = s.token(AUDIENCE_IAM), s.token(AUDIENCE_DATA), s.refresh_token

    clock.now = TOKEN_LIFETIME - TOKEN_REFRESH_MARGIN + 1

    assert s.token(AUDIENCE_IAM) != iam_token
    assert s.refresh_token != before, "the rotated refresh token was not kept"
    assert s.token(AUDIENCE_DATA) != data_token
    assert (iam.iam.refreshes, iam.iam.exchanges) == (1, 2)


def test_session_signs_in_again_when_the_refresh_token_expires(iam: Served) -> None:
    s = Session.sign_in(iam.url, "admin", "secret")
    iam.iam.refuse_refresh = True
    assert s.token(AUDIENCE_ADMIN)
    assert iam.iam.logins == 2


def test_session_from_refresh_token_cannot_sign_in_again(iam: Served) -> None:
    s = Session.from_refresh_token(iam.url, "unknown")
    with pytest.raises(ConnectError) as err:
        s.token(AUDIENCE_DATA)
    assert err.value.code == Code.UNAUTHENTICATED
    assert iam.iam.logins == 0
    with pytest.raises(ValueError):
        Session.from_refresh_token(iam.url, "")


def test_session_mints_once_for_concurrent_callers(iam: Served) -> None:
    s = Session.sign_in(iam.url, "admin", "secret")
    threads = [
        threading.Thread(target=s.token, args=(AUDIENCE_DATA,)) for _ in range(PARALLEL_CALLERS)
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert iam.iam.exchanges == 1


def test_a_refused_token_is_retried_once(iam: Served) -> None:
    s = Session.sign_in(iam.url, "admin", "secret")
    client = Client(iam.url, token_source=s, audience=AUDIENCE_IAM)
    health = HealthServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    )

    iam.iam.refuse_next_calls = 1
    health.get_version(health_service_pb2.GetVersionRequest())
    first, second = iam.iam.seen_tokens
    assert first != second and first.startswith("Bearer ")

    iam.iam.refuse_next_calls = 2
    with pytest.raises(ConnectError) as err:
        health.get_version(health_service_pb2.GetVersionRequest())
    assert err.value.code == Code.UNAUTHENTICATED
    assert len(iam.iam.seen_tokens) == 4, "a refused token is retried once, not more"


def test_static_token(iam: Served) -> None:
    client = Client(iam.url, token_source=StaticToken("paladin_pat_abc"), audience=AUDIENCE_DATA)
    HealthServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    ).get_version(health_service_pb2.GetVersionRequest())
    assert iam.iam.seen_tokens == ["Bearer paladin_pat_abc"]
    with pytest.raises(ValueError):
        StaticToken("")


def test_token_source_needs_an_audience(iam: Served) -> None:
    with pytest.raises(ValueError):
        Client(iam.url, token_source=StaticToken("t"))


def test_async_session(iam: Served) -> None:
    async def run() -> None:
        s = await AsyncSession.sign_in(iam.url, "admin", "secret")
        client = Client(iam.url, token_source=s, audience=AUDIENCE_IAM)
        iam.iam.refuse_next_calls = 1
        async with HealthServiceClient(
            client.base_url,
            interceptors=client.async_interceptors(),
            http_client=client.async_http_client(),
        ) as health:
            await health.get_version(health_service_pb2.GetVersionRequest())
        await asyncio.gather(*(s.token(AUDIENCE_DATA) for _ in range(PARALLEL_CALLERS)))

    asyncio.run(run())
    assert len(iam.iam.seen_tokens) == 2
    assert iam.iam.exchanges == 1
