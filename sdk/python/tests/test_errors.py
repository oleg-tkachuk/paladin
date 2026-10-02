"""Typed errors, against a server that fails with a chosen error, details and
release header — and one that does not serve the procedure at all."""

from __future__ import annotations

import asyncio
import io
import json
import threading
from collections.abc import Iterator
from dataclasses import dataclass
from wsgiref.simple_server import WSGIRequestHandler, make_server

import pytest
from connectrpc.client import ResponseMetadata
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.request import RequestContext
from google.rpc import error_details_pb2

from paladin import (
    ERROR_DOMAIN,
    HEADER_RETRY_AFTER,
    HEADER_SERVER_VERSION,
    AlreadyExistsError,
    Client,
    ContractSkewError,
    Endpoints,
    FailedPreconditionError,
    NotFoundError,
    PaladinError,
    PermissionDeniedError,
    ResourceExhaustedError,
    Retry,
    UnauthenticatedError,
    VersionConflictError,
    connect,
    connect_async,
    reason,
)
from paladin.common.v1 import error_reason_pb2
from paladin.iam.v1 import health_service_pb2
from paladin.iam.v1.health_service_connect import (
    HealthServiceClientSync,
    HealthServiceSync,
    HealthServiceWSGIApplication,
)

SERVER_VERSION = "4.99.0"
PROCEDURE = "/paladin.iam.v1.HealthService/GetVersion"
_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_BUCKET = error_reason_pb2.ERROR_REASON_BUCKET_NOT_FOUND
_UNSPECIFIED = error_reason_pb2.ERROR_REASON_UNSPECIFIED
# Keeps retry tests fast.
FAST = Retry(attempts=3, base_delay=0.001, max_delay=0.001)


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


def info(domain: str, why: str) -> error_details_pb2.ErrorInfo:
    return error_details_pb2.ErrorInfo(domain=domain, reason=why)


@dataclass
class Failing(HealthServiceSync):
    """Fails GetVersion with ``error`` until ``failures`` runs out, then
    succeeds; names its release and ``retry_after`` on every answer."""

    error: ConnectError
    failures: int = 1
    retry_after: str | None = None

    def get_version(self, request, ctx: RequestContext):  # type: ignore[no-untyped-def]
        ctx.response_headers()[HEADER_SERVER_VERSION] = SERVER_VERSION
        if self.retry_after is not None:
            ctx.response_headers()[HEADER_RETRY_AFTER] = self.retry_after
        if self.failures > 0:
            self.failures -= 1
            raise self.error
        return health_service_pb2.VersionInfo()


def _unimplemented(environ, start_response):  # type: ignore[no-untyped-def]
    """What the server answers for a procedure its release lacks: a Connect
    Unimplemented, with its release."""
    body = json.dumps({"code": "unimplemented", "message": "not served"}).encode()
    start_response(
        "404 Not Found",
        [("Content-Type", "application/json"), (HEADER_SERVER_VERSION, SERVER_VERSION)],
    )
    return [body]


@pytest.fixture
def serve() -> Iterator[object]:
    servers = []

    def start(failing: Failing | None) -> str:
        app = HealthServiceWSGIApplication(failing) if failing else None

        def route(environ, start_response):  # type: ignore[no-untyped-def]
            length = int(environ.get("CONTENT_LENGTH") or 0)
            environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
            if app is None:
                return _unimplemented(environ, start_response)
            return app(environ, start_response)

        httpd = make_server(_LOOPBACK, _EPHEMERAL_PORT, route, handler_class=_Quiet)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        servers.append(httpd)
        return f"http://{_LOOPBACK}:{httpd.server_port}"

    yield start
    for httpd in servers:
        httpd.shutdown()
        httpd.server_close()


def _call(url: str, retry: Retry | None = None) -> None:
    connect(Endpoints(iam=url), retry=retry).iam.health.get_version(
        health_service_pb2.GetVersionRequest()
    )


@pytest.mark.parametrize(
    ("code", "details", "kind", "want_reason"),
    [
        (
            Code.NOT_FOUND,
            [info(ERROR_DOMAIN, error_reason_pb2.ErrorReason.Name(_BUCKET))],
            NotFoundError,
            _BUCKET,
        ),
        (
            Code.NOT_FOUND,
            [info(ERROR_DOMAIN, "ERROR_REASON_FROM_THE_FUTURE")],
            NotFoundError,
            _UNSPECIFIED,
        ),
        (
            Code.NOT_FOUND,
            [info("elsewhere", error_reason_pb2.ErrorReason.Name(_BUCKET))],
            NotFoundError,
            _UNSPECIFIED,
        ),
        (Code.ALREADY_EXISTS, [], AlreadyExistsError, _UNSPECIFIED),
        (Code.PERMISSION_DENIED, [], PermissionDeniedError, _UNSPECIFIED),
        (Code.FAILED_PRECONDITION, [], FailedPreconditionError, _UNSPECIFIED),
        (Code.ABORTED, [], VersionConflictError, _UNSPECIFIED),
        (Code.UNAUTHENTICATED, [], UnauthenticatedError, _UNSPECIFIED),
    ],
    ids=[
        "not found, with its reason",
        "a reason this SDK predates",
        "another domain's reason",
        "already exists",
        "permission denied",
        "failed precondition",
        "aborted is a version conflict",
        "unauthenticated",
    ],
)
def test_errors_are_typed_and_carry_the_reason(  # type: ignore[no-untyped-def]
    serve, code, details, kind, want_reason
) -> None:
    url = serve(Failing(ConnectError(code, "refused", details)))
    with pytest.raises(kind) as err:
        _call(url)
    e = err.value
    assert isinstance(e, ConnectError) and e.code == code
    assert reason(e) == want_reason
    assert (e.procedure, e.server_version) == (PROCEDURE, SERVER_VERSION)
    assert e.sdk_version
    assert len(e.decoded_details) == len(details)


def test_resource_exhausted_carries_retry_after(serve) -> None:  # type: ignore[no-untyped-def]
    url = serve(Failing(ConnectError(Code.RESOURCE_EXHAUSTED, "slow down"), retry_after="7"))
    with pytest.raises(ResourceExhaustedError) as err:
        _call(url)
    assert err.value.retry_after == 7.0


def test_a_code_with_no_kind_stays_a_connect_error(serve) -> None:  # type: ignore[no-untyped-def]
    url = serve(Failing(ConnectError(Code.INTERNAL, "boom")))
    with pytest.raises(ConnectError) as err:
        _call(url)
    assert not isinstance(err.value, PaladinError)


def test_contract_skew_names_the_procedure_and_both_versions(serve) -> None:  # type: ignore[no-untyped-def]
    with pytest.raises(ContractSkewError) as err:
        _call(serve(None))
    message = str(err.value)
    assert PROCEDURE in message and SERVER_VERSION in message and "sdk" in message


def test_retries_see_the_connect_error_and_the_caller_the_typed_one(serve) -> None:  # type: ignore[no-untyped-def]
    failing = Failing(ConnectError(Code.UNAVAILABLE, "down"), failures=1)
    _call(serve(failing), FAST)
    assert failing.failures == 0
    failing = Failing(ConnectError(Code.NOT_FOUND, "gone"), failures=5)
    with pytest.raises(NotFoundError):
        _call(serve(failing), FAST)


@pytest.mark.parametrize("retry", [None, FAST], ids=["without retries", "with retries"])
def test_a_callers_response_metadata_still_sees_the_headers(serve, retry) -> None:  # type: ignore[no-untyped-def]
    # Guards errors.relayed, which reaches into connect-python: the SDK's own
    # ResponseMetadata must hand the headers on to the caller's.
    url = serve(Failing(ConnectError(Code.UNAVAILABLE, "down"), failures=0))
    client = Client(url, retry=retry)
    health = HealthServiceClientSync(client.base_url, interceptors=client.interceptors())
    with ResponseMetadata() as meta:
        health.get_version(health_service_pb2.GetVersionRequest())
    assert meta.headers().get(HEADER_SERVER_VERSION.lower()) == SERVER_VERSION


def test_async_errors_are_typed(serve) -> None:  # type: ignore[no-untyped-def]
    url = serve(Failing(ConnectError(Code.NOT_FOUND, "gone")))
    p = connect_async(Endpoints(iam=url))

    async def call() -> None:
        await p.iam.health.get_version(health_service_pb2.GetVersionRequest())

    with pytest.raises(NotFoundError) as err:
        asyncio.run(call())
    assert err.value.server_version == SERVER_VERSION


def test_reason_of_another_error() -> None:
    assert reason(ValueError("plain")) == _UNSPECIFIED
