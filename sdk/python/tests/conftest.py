"""An IAM server that records what it was sent and fails on demand."""

from __future__ import annotations

import importlib.util
import io
import threading
from collections.abc import Iterator
from dataclasses import dataclass, field
from wsgiref.simple_server import WSGIRequestHandler, WSGIServer, make_server

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.request import RequestContext
from data_plane_fake import Fake, serving

from paladin.iam.v1 import auth_service_pb2, health_service_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceSync, AuthServiceWSGIApplication
from paladin.iam.v1.health_service_connect import HealthServiceSync, HealthServiceWSGIApplication

# The modules that make TLS connections, through the tls extra's HTTP stack or
# a server built on it; a wheel installed without the extra skips them, and
# test_tls_extra.py checks what such an install does instead.
_NEEDS_TLS_EXTRA = ["test_tls.py", "test_tls_http.py", "test_tls_identity.py", "test_lifecycle.py"]
collect_ignore = [] if importlib.util.find_spec("httpcore") else _NEEDS_TLS_EXTRA

# Any free port.
_EPHEMERAL_PORT = 0
_LOOPBACK = "127.0.0.1"


@dataclass
class Recorder:
    """Headers of every call, and how many of the next calls to fail."""

    headers: list[dict[str, str]] = field(default_factory=list)
    failures: int = 0
    fail_code: Code = Code.UNAVAILABLE
    # Sent as Retry-After with each injected failure, when set.
    retry_after: str | None = None
    lock: threading.Lock = field(default_factory=threading.Lock)

    def record(self, ctx: RequestContext) -> None:
        with self.lock:
            self.headers.append({k.lower(): v for k, v in ctx.request_headers().items()})
            if self.failures > 0:
                self.failures -= 1
                if self.retry_after is not None:
                    ctx.response_headers()["Retry-After"] = self.retry_after
                raise ConnectError(self.fail_code, "injected")

    @property
    def calls(self) -> int:
        with self.lock:
            return len(self.headers)

    @property
    def last(self) -> dict[str, str]:
        with self.lock:
            return self.headers[-1]


class _Health(HealthServiceSync):
    def __init__(self, rec: Recorder) -> None:
        self._rec = rec

    def get_version(
        self, request: health_service_pb2.GetVersionRequest, ctx: RequestContext
    ) -> health_service_pb2.VersionInfo:
        self._rec.record(ctx)
        return health_service_pb2.VersionInfo()


class _Auth(AuthServiceSync):
    def __init__(self, rec: Recorder) -> None:
        self._rec = rec

    def login(
        self, request: auth_service_pb2.LoginRequest, ctx: RequestContext
    ) -> auth_service_pb2.LoginResponse:
        self._rec.record(ctx)
        return auth_service_pb2.LoginResponse()


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


@dataclass
class Server:
    url: str
    recorder: Recorder


@pytest.fixture
def server() -> Iterator[Server]:
    rec = Recorder()
    apps = [HealthServiceWSGIApplication(_Health(rec)), AuthServiceWSGIApplication(_Auth(rec))]

    def route(environ, start_response):  # type: ignore[no-untyped-def]
        # wsgiref hands over the raw socket, which blocks past the body; the
        # server drains the body after an error, so it must see where it ends.
        length = int(environ.get("CONTENT_LENGTH") or 0)
        environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
        path = environ.get("PATH_INFO", "")
        for app in apps:
            if path.startswith(app.path + "/"):
                return app(environ, start_response)
        start_response("404 Not Found", [("Content-Type", "text/plain")])
        return [b"no such service"]

    httpd: WSGIServer = make_server(_LOOPBACK, _EPHEMERAL_PORT, route, handler_class=_Quiet)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    try:
        yield Server(url=f"http://{_LOOPBACK}:{httpd.server_port}", recorder=rec)
    finally:
        httpd.shutdown()
        httpd.server_close()


@pytest.fixture
def fake() -> Iterator[Fake]:
    """A fake data plane and its storage; see data_plane_fake."""
    with serving() as f:
        yield f
