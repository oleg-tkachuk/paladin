"""The SDK's TLS transports behave as pyqwest's do where a caller can tell:
the call's deadline, the errors raised, a compressed response, OpenTelemetry,
and the pyqwest-only settings kept for a deprecation cycle."""

from __future__ import annotations

import gzip
import time
import warnings
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import httpcore
import pyqwest
import pytest
from certs import Authority, write
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from tls_server import TLSServer, serve

from paladin import TLS, Endpoints, Transfer, connect
from paladin._tls_http import (
    Settings,
    _decoder,
    _pyqwest_errors,
    _response_parts,
    _timeouts,
    call_deadline,
)
from paladin.common.v1 import resource_pb2
from paladin.iam.v1 import health_service_pb2

# A call deadline far shorter than any transport default, and the bound on
# how long the refused call may take to come back.
CALL_TIMEOUT_MS = 200
CALL_RETURNS_WITHIN = 5.0
READ_TIMEOUT = 30.0
BODY = b"a compressed Connect response"


@pytest.fixture
def servers(tmp_path: Path) -> Iterator[Any]:
    started: list[TLSServer] = []

    def start(*args: Any, **kw: Any) -> TLSServer:
        srv = serve(tmp_path, *args, **kw)
        started.append(srv)
        return srv

    yield start
    for srv in started:
        srv.stop()


def _tls(tmp_path: Path, ca: Authority) -> TLS:
    f = write(tmp_path, ca.pem, ca.issue(client=True, ips=False))
    return TLS(ca_file=f.ca, cert_file=f.cert, key_file=f.key)


def test_the_call_deadline_bounds_a_server_that_never_answers(servers: Any, tmp_path: Path) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False), h2=False, silent=True)
    health = connect(Endpoints(iam=srv.url), tls=_tls(tmp_path, ca), transport={}).iam.health
    started = time.monotonic()
    with pytest.raises(ConnectError) as err:
        health.get_version(health_service_pb2.GetVersionRequest(), timeout_ms=CALL_TIMEOUT_MS)
    assert time.monotonic() - started < CALL_RETURNS_WITHIN
    # connectrpc reads a TimeoutError as the deadline, as from pyqwest.
    assert err.value.code == Code.DEADLINE_EXCEEDED


def test_timeouts_are_capped_by_the_deadline() -> None:
    settings = Settings(read_timeout=READ_TIMEOUT)
    assert _timeouts(settings)["read"] == READ_TIMEOUT
    token = call_deadline.set(time.monotonic() + CALL_TIMEOUT_MS / 1000)
    try:
        assert _timeouts(settings)["read"] <= CALL_TIMEOUT_MS / 1000
    finally:
        call_deadline.reset(token)


@pytest.mark.parametrize(
    ("theirs", "ours"),
    [
        (httpcore.ConnectTimeout, pyqwest.ConnectTimeout),
        (httpcore.ReadTimeout, TimeoutError),
        (httpcore.ConnectError, ConnectionError),
        (httpcore.ReadError, pyqwest.ReadError),
        (httpcore.WriteError, pyqwest.WriteError),
        (httpcore.RemoteProtocolError, pyqwest.RemoteProtocolError),
    ],
)
def test_errors_are_pyqwests(theirs: type[Exception], ours: type[Exception]) -> None:
    with pytest.raises(ours) as err, _pyqwest_errors():
        raise theirs("x")
    assert isinstance(err.value.__cause__, theirs)


@pytest.mark.parametrize("encoding", ["gzip", "identity"])
def test_a_compressed_response_is_decoded(encoding: str) -> None:
    body = gzip.compress(BODY) if encoding == "gzip" else BODY
    resp = httpcore.Response(
        200, headers=[(b"content-encoding", encoding.encode()), (b"content-length", b"9")]
    )
    _, headers, decode = _response_parts(resp)
    if encoding == "gzip":
        assert decode is not None
        half = len(body) // 2
        assert decode(body[:half], False) + decode(body[half:], True) == BODY
        assert headers.get("content-encoding") is None
        assert headers.get("content-length") is None
    else:
        assert decode is None and _decoder(encoding) is None


def test_opentelemetry_over_tls(servers: Any, tmp_path: Path) -> None:
    sdk_trace = pytest.importorskip("opentelemetry.sdk.trace")
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

    spans = InMemorySpanExporter()
    provider = sdk_trace.TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(spans))
    ca = Authority()
    srv = servers(ca, ca.issue(client=False), h2=True)
    transfer = Transfer(tls=_tls(tmp_path, ca), otel=True, tracer_provider=provider)
    signed = resource_pb2.PresignedUrl(url=f"{srv.url}/bucket/key?X-Amz-Signature=secret")
    with transfer.stream("PUT", signed, content=b"x") as resp:
        assert resp.status == 200
    [span] = spans.get_finished_spans()
    assert span.attributes["http.request.method"] == "PUT"
    assert span.attributes["http.response.status_code"] == 200
    assert "secret" not in span.attributes["url.full"], "the signature reached the span"


def test_pyqwest_only_settings_are_deprecated(tmp_path: Path) -> None:
    ca = Authority()
    tls = _tls(tmp_path, ca)
    with pytest.warns(DeprecationWarning, match="deprecated"):
        transport = tls.sync_transport(enable_gzip=False)
    assert hasattr(transport, "execute_sync")
    # What only the SDK's transports do cannot be had with them.
    f = write(tmp_path, ca.pem, ca.issue(client=True, ips=False))
    identified = TLS(ca_file=f.ca, server_id="spiffe://test.example/x")
    with pytest.raises(TypeError):
        identified.sync_transport(enable_gzip=False)
    with warnings.catch_warnings():
        warnings.simplefilter("error")
        tls.sync_transport(connect_timeout=1.0, read_timeout=READ_TIMEOUT)
