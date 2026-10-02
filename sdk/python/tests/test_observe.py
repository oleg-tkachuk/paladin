"""Hooks, logging, the User-Agent suffix, and OpenTelemetry through the
extension points — against the fake IAM and the fake data plane."""

from __future__ import annotations

import logging

import pyqwest
import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from data_plane_fake import Fake

from paladin import (
    HEADER_USER_AGENT,
    Client,
    Endpoints,
    Hooks,
    IntegrityError,
    Retry,
    RetryEvent,
    Transfer,
    TransferError,
    TransferEvent,
    connect,
    download,
    upload,
)
from paladin.data.v1 import types_pb2
from paladin.iam.v1 import health_service_pb2
from paladin.iam.v1.health_service_connect import HealthServiceClientSync
from paladin.observe import LOGGER_NAME

FAST = Retry(attempts=3, base_delay=0.001, max_delay=0.001)
PARENT = "tenants/t/collections/c"


def _health(client: Client) -> HealthServiceClientSync:
    return HealthServiceClientSync(client.base_url, interceptors=client.interceptors())


def test_user_agent_suffix(server) -> None:  # type: ignore[no-untyped-def]
    _health(Client(server.url, user_agent_suffix="worker/2.1")).get_version(
        health_service_pb2.GetVersionRequest()
    )
    agent = server.recorder.last[HEADER_USER_AGENT.lower()]
    assert agent.startswith("paladin-sdk-python/") and agent.endswith(" worker/2.1")


def test_retries_reach_hooks_and_the_logger(server, caplog: pytest.LogCaptureFixture) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 2
    events: list[RetryEvent] = []
    client = Client(server.url, retry=FAST, hooks=Hooks(on_retry=events.append))
    with caplog.at_level(logging.DEBUG, logger=LOGGER_NAME):
        _health(client).get_version(health_service_pb2.GetVersionRequest())
    assert [e.attempt for e in events] == [1, 2]
    assert events[0].procedure == "/paladin.iam.v1.HealthService/GetVersion"
    assert isinstance(events[0].error, ConnectError) and events[0].error.code == Code.UNAVAILABLE
    records = [r for r in caplog.records if r.name == LOGGER_NAME]
    assert len(records) == 2 and records[0].levelno == logging.DEBUG
    assert records[0].attempt == 1  # type: ignore[attr-defined]


def test_transfers_reach_hooks_and_the_logger(fake: Fake, caplog: pytest.LogCaptureFixture) -> None:
    events: list[TransferEvent] = []
    data = connect(
        Endpoints(data=fake.base), transfer=Transfer(hooks=Hooks(on_transfer=events.append))
    ).data
    body = b"counted bytes"
    with caplog.at_level(logging.DEBUG, logger=LOGGER_NAME):
        obj = upload(
            data, parent=PARENT, key="k", content_type="text/plain", body=body, size=len(body)
        )
        assert download(data, obj.name) == body
        fake.described = types_pb2.Object(name=obj.name, size_bytes=len(body) + 1)
        with pytest.raises(IntegrityError):
            download(data, obj.name)
        fake.refuse_with = (403, b"no")
        with pytest.raises(TransferError):
            download(data, obj.name)
    put, get, short, refused = events
    assert (put.method, put.bytes, put.error) == ("PUT", len(body), None) and put.host
    assert (get.method, get.bytes, get.error) == ("GET", len(body), None)
    assert isinstance(short.error, IntegrityError)
    assert isinstance(refused.error, TransferError) and refused.error.status == 403
    levels = [r.levelno for r in caplog.records if r.name == LOGGER_NAME]
    assert levels == [logging.DEBUG, logging.DEBUG, logging.WARNING, logging.WARNING]


def test_extra_interceptors_run_outside_the_sdks_own(server) -> None:  # type: ignore[no-untyped-def]
    seen: list[str] = []

    class Timing:
        def intercept_unary_sync(self, call_next, request, ctx):  # type: ignore[no-untyped-def]
            seen.append("before")
            try:
                return call_next(request, ctx)
            finally:
                seen.append("after")

    server.recorder.failures = 1
    _health(Client(server.url, retry=FAST, interceptors=[Timing()])).get_version(
        health_service_pb2.GetVersionRequest()
    )
    assert seen == ["before", "after"], "an outer interceptor saw the retry, not the call"
    assert server.recorder.calls == 2


def _provider():  # type: ignore[no-untyped-def]
    sdk_trace = pytest.importorskip("opentelemetry.sdk.trace")
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

    spans = InMemorySpanExporter()
    provider = sdk_trace.TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(spans))
    return provider, spans


def test_opentelemetry_on_the_rpc_leg(server) -> None:  # type: ignore[no-untyped-def]
    # The HTTP stack's own instrumentation, through the http_client the
    # generated clients take.
    provider, spans = _provider()
    http = pyqwest.SyncClient(pyqwest.SyncHTTPTransport(tracer_provider=provider))
    connect(Endpoints(iam=server.url), transport={"http_client": http}).iam.health.get_version(
        health_service_pb2.GetVersionRequest()
    )
    assert server.recorder.last.get("traceparent"), "the RPC carried no traceparent"
    assert spans.get_finished_spans()


def test_opentelemetry_on_the_presigned_leg(fake: Fake) -> None:
    provider, spans = _provider()
    data = connect(
        Endpoints(data=fake.base), transfer=Transfer(otel=True, tracer_provider=provider)
    ).data
    upload(data, parent=PARENT, key="k", content_type="text/plain", body=b"x", size=1)
    assert fake.traceparents >= 1, "storage saw no traceparent"
    assert spans.get_finished_spans()
