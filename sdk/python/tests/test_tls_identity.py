"""TLS identity and parity with the Go SDK: the server's SPIFFE ID,
``verify_peer``, ``min_version``, and connections a rotation replaced — over
HTTP/1.1 with keep-alive and over HTTP/2, on every path that dials."""

from __future__ import annotations

import asyncio
import json
import os
import ssl
import time
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
from certs import Authority, Files, write
from connectrpc.errors import ConnectError
from cryptography import x509
from tls_server import TLSServer, serve

from paladin import (
    TLS,
    Endpoints,
    NoCAError,
    ServerIDError,
    ServerIDNeedsCAError,
    TLSAndHTTPError,
    TLSKeyPairError,
    TLSMinVersionError,
    Transfer,
    connect,
    connect_async,
)
from paladin.common.v1 import resource_pb2
from paladin.iam.v1 import health_service_pb2
from paladin.tls import parse_spiffe_id

SPIFFE_IDS = Path(__file__).resolve().parents[2] / "testdata" / "spiffe_ids.json"
SERVER_ID = "spiffe://test.example/ns/paladin/sa/paladin-core"
OTHER_ID = "spiffe://test.example/ns/elsewhere/sa/impostor"
# Re-read the files on every call.
RELOAD_NOW = 0.0
# Moves a rewritten file's mtime past the file system's granularity.
LATER = 60
# How long a replaced connection may take to close, and how often to look.
CLOSE_WAIT = 5.0
CLOSE_POLL = 0.01
PUT = "PUT"
PROTOCOLS = pytest.mark.parametrize("h2", [False, True], ids=["HTTP/1.1", "HTTP/2"])


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


def _client_files(tmp_path: Path, ca: Authority) -> Files:
    d = tmp_path / "client"
    d.mkdir(exist_ok=True)
    return write(d, ca.pem, ca.issue(client=True, ips=False))


def _tls(f: Files, **kw: Any) -> TLS:
    return TLS(ca_file=f.ca, cert_file=f.cert, key_file=f.key, **kw)


def _get_version(url: str, tls: TLS) -> None:
    connect(Endpoints(iam=url), tls=tls).iam.health.get_version(
        health_service_pb2.GetVersionRequest()
    )


def _put(url: str, tls: TLS) -> None:
    signed = resource_pb2.PresignedUrl(url=f"{url}/bucket/key", method=PUT)
    with Transfer(tls=tls).stream(PUT, signed, content=b"body") as resp:
        assert resp.headers.get("etag")


def _aget_version(url: str, tls: TLS) -> None:
    async def call() -> None:
        p = connect_async(Endpoints(iam=url), tls=tls)
        await p.iam.health.get_version(health_service_pb2.GetVersionRequest())

    asyncio.run(call())


def _aput(url: str, tls: TLS) -> None:
    async def call() -> None:
        signed = resource_pb2.PresignedUrl(url=f"{url}/bucket/key", method=PUT)
        async with Transfer(tls=tls).astream(PUT, signed, content=b"body") as resp:
            assert resp.headers.get("etag")

    asyncio.run(call())


# Every path that dials Paladin or storage.
PATHS = pytest.mark.parametrize(
    "call",
    [_get_version, _aget_version, _put, _aput],
    ids=["connect", "connect_async", "Transfer", "Transfer async"],
)


def _cause(err: BaseException, kind: type[BaseException]) -> BaseException | None:
    """The first exception of ``kind`` in ``err``'s chain."""
    seen: BaseException | None = err
    while seen is not None:
        if isinstance(seen, kind):
            return seen
        seen = seen.__cause__ or seen.__context__
    return None


def _touch_later(*paths: Path) -> None:
    later = time.time() + LATER
    for p in paths:
        os.utime(p, (later, later))


# ─── The SPIFFE ID and the arguments ────────────────────────────────────────


def test_spiffe_ids_are_judged_as_go_spiffe_judges_them() -> None:
    table = json.loads(SPIFFE_IDS.read_text())
    assert table["valid"] and table["invalid"], "the table is empty"
    for value in table["valid"]:
        assert parse_spiffe_id(value) == value
    for value in table["invalid"]:
        with pytest.raises(ServerIDError):
            parse_spiffe_id(value)


def test_tls_argument_errors(tmp_path: Path) -> None:
    ca = Authority()
    f = _client_files(tmp_path, ca)
    with pytest.raises(TLSKeyPairError):
        TLS(cert_file=f.cert)
    with pytest.raises(ServerIDError):
        TLS(ca_file=f.ca, server_id="https://paladin.example")
    with pytest.raises(ServerIDNeedsCAError):
        TLS(server_id=SERVER_ID)
    with pytest.raises(TLSMinVersionError):
        TLS(min_version=ssl.TLSVersion.TLSv1_1)
    not_pem = tmp_path / "ca.txt"
    not_pem.write_text("not a certificate")
    with pytest.raises(NoCAError):
        TLS(ca_file=not_pem).sync_transport()
    with pytest.raises(TLSAndHTTPError):
        Transfer(tls=_tls(f), transport=object())
    # Each is the ValueError it was before it had a name of its own.
    assert issubclass(TLSKeyPairError, ValueError)


# ─── The server's SPIFFE ID ─────────────────────────────────────────────────


@PATHS
@PROTOCOLS
def test_the_expected_spiffe_id_connects(servers: Any, tmp_path: Path, call: Any, h2: bool) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False, ips=False, uris=(SERVER_ID,)), h2=h2)
    f = _client_files(tmp_path, ca)
    call(srv.url, _tls(f, server_id=SERVER_ID))
    assert len(srv.seen) == 1


@PATHS
@PROTOCOLS
def test_another_spiffe_id_is_refused_before_a_request(
    servers: Any, tmp_path: Path, call: Any, h2: bool
) -> None:
    # The same trust domain and the same CA: only the ID tells them apart.
    ca = Authority()
    srv = servers(ca, ca.issue(client=False, ips=False, uris=(OTHER_ID,)), h2=h2)
    f = _client_files(tmp_path, ca)
    with pytest.raises(Exception) as err:
        call(srv.url, _tls(f, server_id=SERVER_ID))
    assert _cause(err.value, ServerIDError) is not None, err.value
    assert not srv.seen, "a request reached the server it was refused for"


def test_without_a_server_id_the_host_name_is_checked(servers: Any, tmp_path: Path) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False, ips=False, uris=(SERVER_ID,)), h2=False)
    with pytest.raises(ConnectError):
        _get_version(srv.url, _tls(_client_files(tmp_path, ca)))
    assert not srv.seen


def test_verify_peer_runs_after_the_spiffe_id_and_can_refuse(servers: Any, tmp_path: Path) -> None:
    ca = Authority()
    f = _client_files(tmp_path, ca)
    seen: list[x509.Certificate] = []

    def audit(leaf: x509.Certificate) -> None:
        seen.append(leaf)

    good = servers(ca, ca.issue(client=False, ips=False, uris=(SERVER_ID,)), h2=True)
    _get_version(good.url, _tls(f, server_id=SERVER_ID, verify_peer=audit))
    assert len(seen) == 1
    sans = seen[0].extensions.get_extension_for_class(x509.SubjectAlternativeName).value
    assert sans.get_values_for_type(x509.UniformResourceIdentifier) == [SERVER_ID]

    bad = servers(ca, ca.issue(client=False, ips=False, uris=(OTHER_ID,)), h2=True)
    with pytest.raises(ConnectError):
        _get_version(bad.url, _tls(f, server_id=SERVER_ID, verify_peer=audit))
    assert len(seen) == 1, "verify_peer saw a server whose SPIFFE ID was refused"

    class Refused(Exception):
        pass

    def refuse(leaf: x509.Certificate) -> None:
        raise Refused

    with pytest.raises(ConnectError) as err:
        _get_version(good.url, _tls(f, server_id=SERVER_ID, verify_peer=refuse))
    assert _cause(err.value, Refused) is not None


@PROTOCOLS
def test_a_rotation_keeps_verifying_the_spiffe_id(servers: Any, tmp_path: Path, h2: bool) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False, ips=False, uris=(SERVER_ID,)), h2=h2)
    f = _client_files(tmp_path, ca)
    second = ca.issue(client=True, ips=False)
    health = connect(
        Endpoints(iam=srv.url), tls=_tls(f, server_id=SERVER_ID, reload_interval=RELOAD_NOW)
    ).iam.health
    health.get_version(health_service_pb2.GetVersionRequest())
    f.write_client(second)
    _touch_later(f.cert, f.key)
    health.get_version(health_service_pb2.GetVersionRequest())
    assert srv.serials()[-1] == second.serial
    # The files rotate to a bundle the server's SVID no longer chains to.
    f.ca.write_bytes(Authority("another trust domain").pem)
    _touch_later(f.ca)
    with pytest.raises(ConnectError):
        health.get_version(health_service_pb2.GetVersionRequest())


# ─── min_version ────────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    ("minimum", "ok"),
    [(None, True), (ssl.TLSVersion.TLSv1_2, True), (ssl.TLSVersion.TLSv1_3, False)],
    ids=["default", "1.2", "1.3"],
)
def test_min_version_against_a_tls_1_2_server(
    servers: Any, tmp_path: Path, minimum: ssl.TLSVersion | None, ok: bool
) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False), h2=False, max_version=ssl.TLSVersion.TLSv1_2)
    f = _client_files(tmp_path, ca)
    tls = _tls(f) if minimum is None else _tls(f, min_version=minimum)
    if ok:
        _get_version(srv.url, tls)
        assert len(srv.seen) == 1
    else:
        with pytest.raises(ConnectError):
            _get_version(srv.url, tls)
        assert not srv.seen


# ─── Connections a rotation replaced ────────────────────────────────────────


def _closed_within(srv: TLSServer, conn: int) -> bool:
    deadline = time.monotonic() + CLOSE_WAIT
    while time.monotonic() < deadline:
        if srv.is_closed(conn):
            return True
        time.sleep(CLOSE_POLL)
    return False


@PROTOCOLS
def test_no_request_goes_on_a_connection_a_rotation_replaced(
    servers: Any, tmp_path: Path, h2: bool
) -> None:
    ca = Authority()
    srv = servers(ca, ca.issue(client=False), h2=h2)
    f = _client_files(tmp_path, ca)
    first_serial = None
    second = ca.issue(client=True, ips=False)
    health = connect(Endpoints(iam=srv.url), tls=_tls(f, reload_interval=RELOAD_NOW)).iam.health
    for _ in range(2):
        health.get_version(health_service_pb2.GetVersionRequest())
    conns = srv.conns()
    assert conns[0] == conns[1], "the connection was not kept alive: nothing to test"
    first_serial = srv.serials()[0]
    f.write_client(second)
    _touch_later(f.cert, f.key)
    for _ in range(2):
        health.get_version(health_service_pb2.GetVersionRequest())
    serials, conns = srv.serials(), srv.conns()
    assert serials == [first_serial, first_serial, second.serial, second.serial]
    assert conns[2] != conns[0] and conns[3] == conns[2]
    assert _closed_within(srv, conns[0]), "the connection on the old certificate stays open"
    assert srv.protocols[0] == ("h2" if h2 else "http/1.1")
