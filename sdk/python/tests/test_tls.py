"""TLS: the RPC leg and the presigned leg over mutual TLS, with files that
rotate on disk — against servers that require a client certificate."""

from __future__ import annotations

import asyncio
import os
import ssl
import threading
import time
from collections.abc import Iterator
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from wsgiref.simple_server import WSGIRequestHandler, make_server

import pyqwest
import pytest
from certs import Authority, Files, Leaf, write
from connectrpc.errors import ConnectError
from cryptography import x509

from paladin import TLS, Endpoints, Transfer, connect, connect_async
from paladin.common.v1 import resource_pb2
from paladin.iam.v1 import health_service_pb2
from paladin.iam.v1.health_service_connect import HealthServiceSync, HealthServiceWSGIApplication

_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_PEER_SERIAL = "paladin.test.peer_serial"
# Re-read the files on every call.
RELOAD_NOW = 0.0
# Moves a rewritten file's mtime past the file system's granularity.
LATER = 60


class _PeerHandler(WSGIRequestHandler):
    """Puts the client certificate's serial into the WSGI environ."""

    def log_message(self, format: str, *args: object) -> None:
        return None

    def get_environ(self) -> dict[str, Any]:
        env = super().get_environ()
        der = self.connection.getpeercert(binary_form=True)  # type: ignore[attr-defined]
        env[_PEER_SERIAL] = x509.load_der_x509_certificate(der).serial_number if der else None
        return env


class _Health(HealthServiceSync):
    def get_version(self, request, ctx):  # type: ignore[no-untyped-def]
        return health_service_pb2.VersionInfo()


@dataclass
class Served:
    url: str
    serials: list[int] = field(default_factory=list)


@pytest.fixture
def serve(tmp_path: Path) -> Iterator[Any]:
    """Serve the health service and a PUT sink over TLS that requires a
    client certificate from ``ca``. HTTP/1.0: every call is a new handshake."""
    servers = []

    def start(ca: Authority, server: Leaf) -> Served:
        cert, key = tmp_path / f"srv-{server.serial}.crt", tmp_path / f"srv-{server.serial}.key"
        cert.write_bytes(server.cert_pem)
        key.write_bytes(server.key_pem)
        ca_file = tmp_path / f"ca-{server.serial}.pem"
        ca_file.write_bytes(ca.pem)
        context = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(cert, key)
        context.load_verify_locations(ca_file)
        context.verify_mode = ssl.CERT_REQUIRED
        served = Served(url="")
        health = HealthServiceWSGIApplication(_Health())

        def app(environ, start_response):  # type: ignore[no-untyped-def]
            served.serials.append(environ[_PEER_SERIAL])
            if environ["REQUEST_METHOD"] == "PUT":
                environ["wsgi.input"].read(int(environ.get("CONTENT_LENGTH") or 0))
                start_response("200 OK", [("ETag", '"e"'), ("Content-Length", "0")])
                return [b""]
            return health(environ, start_response)

        httpd = make_server(_LOOPBACK, _EPHEMERAL_PORT, app, handler_class=_PeerHandler)
        httpd.socket = context.wrap_socket(httpd.socket, server_side=True)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        servers.append(httpd)
        served.url = f"https://{_LOOPBACK}:{httpd.server_port}"
        return served

    yield start
    for httpd in servers:
        httpd.shutdown()
        httpd.server_close()


def _get_version(url: str, tls: TLS) -> None:
    connect(Endpoints(iam=url), tls=tls).iam.health.get_version(
        health_service_pb2.GetVersionRequest()
    )


def _touch_later(*paths: Path) -> None:
    later = time.time() + LATER
    for p in paths:
        os.utime(p, (later, later))


def _tls(f: Files, **kw: Any) -> TLS:
    return TLS(ca_file=f.ca, cert_file=f.cert, key_file=f.key, **kw)


def test_mutual_tls_presents_the_client_certificate(serve: Any, tmp_path: Path) -> None:
    ca = Authority()
    client = ca.issue(client=True, ips=False)
    srv = serve(ca, ca.issue(client=False))
    _get_version(srv.url, _tls(write(tmp_path, ca.pem, client)))
    assert srv.serials == [client.serial]


@pytest.mark.parametrize(
    "case", ["a CA it does not chain to", "a name that is not the host", "no client certificate"]
)
def test_tls_refuses(serve: Any, tmp_path: Path, case: str) -> None:
    ca, other = Authority(), Authority("other CA")
    client = ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, client)
    server = ca.issue(client=False)
    tls = _tls(f)
    if case == "a CA it does not chain to":
        f.ca.write_bytes(other.pem)
        tls = _tls(f)
    elif case == "a name that is not the host":
        server = ca.issue(client=False, ips=False, dns=("elsewhere.example",))
    else:
        tls = TLS(ca_file=f.ca)
    srv = serve(ca, server)
    with pytest.raises(ConnectError):
        _get_version(srv.url, tls)
    assert not srv.serials


def test_a_rotated_client_certificate_is_picked_up(serve: Any, tmp_path: Path) -> None:
    ca = Authority()
    first, second = ca.issue(client=True, ips=False), ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, first)
    srv = serve(ca, ca.issue(client=False))
    health = connect(Endpoints(iam=srv.url), tls=_tls(f, reload_interval=RELOAD_NOW)).iam.health
    health.get_version(health_service_pb2.GetVersionRequest())
    f.write_client(second)
    _touch_later(f.cert, f.key)
    health.get_version(health_service_pb2.GetVersionRequest())
    assert srv.serials == [first.serial, second.serial]


def test_a_half_written_rotation_keeps_the_last_good_pair(serve: Any, tmp_path: Path) -> None:
    ca = Authority()
    first, second = ca.issue(client=True, ips=False), ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, first)
    srv = serve(ca, ca.issue(client=False))
    health = connect(Endpoints(iam=srv.url), tls=_tls(f, reload_interval=RELOAD_NOW)).iam.health
    f.cert.write_bytes(second.cert_pem)  # the new certificate, not yet its key
    _touch_later(f.cert)
    health.get_version(health_service_pb2.GetVersionRequest())
    assert srv.serials == [first.serial]


def test_the_files_are_not_reread_before_the_interval(serve: Any, tmp_path: Path) -> None:
    ca = Authority()
    first, second = ca.issue(client=True, ips=False), ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, first)
    srv = serve(ca, ca.issue(client=False))
    health = connect(Endpoints(iam=srv.url), tls=_tls(f)).iam.health
    f.write_client(second)
    _touch_later(f.cert, f.key)
    health.get_version(health_service_pb2.GetVersionRequest())
    assert srv.serials == [first.serial]


def test_async_connect_over_tls(serve: Any, tmp_path: Path) -> None:
    ca = Authority()
    client = ca.issue(client=True, ips=False)
    srv = serve(ca, ca.issue(client=False))
    p = connect_async(Endpoints(iam=srv.url), tls=_tls(write(tmp_path, ca.pem, client)))

    async def call() -> None:
        await p.iam.health.get_version(health_service_pb2.GetVersionRequest())

    asyncio.run(call())
    assert srv.serials == [client.serial]


def test_transfer_tls_reaches_storage_that_requires_a_client_certificate(
    serve: Any, tmp_path: Path
) -> None:
    ca = Authority()
    client = ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, client)
    srv = serve(ca, ca.issue(client=False))
    signed = resource_pb2.PresignedUrl(url=srv.url + "/storage/k")
    with Transfer(tls=_tls(f)).stream("PUT", signed, {"Content-Length": "1"}, b"x") as resp:
        assert resp.status == 200
    assert srv.serials == [client.serial]
    with (
        pytest.raises((pyqwest.WriteError, pyqwest.ReadError, ConnectionError)),
        Transfer(tls=TLS(ca_file=f.ca)).stream("PUT", signed, {"Content-Length": "1"}, b"x"),
    ):
        pass


def test_tls_argument_errors(tmp_path: Path) -> None:
    ca = Authority()
    f = write(tmp_path, ca.pem, ca.issue(client=True, ips=False))
    with pytest.raises(ValueError):
        TLS(cert_file=f.cert)
    with pytest.raises(ValueError):
        TLS(key_file=f.key)
    with pytest.raises(ValueError):
        connect(Endpoints(iam="https://x"), tls=_tls(f), transport={"http_client": object()})
    with pytest.raises(ValueError):
        Transfer(tls=_tls(f), transport=object())
    with pytest.raises(FileNotFoundError):
        connect(Endpoints(iam="https://x"), tls=TLS(ca_file=tmp_path / "absent.pem"))


@pytest.mark.parametrize(("with_ca", "system"), [(True, False), (False, True)])
def test_a_ca_bundle_replaces_the_system_roots(tmp_path: Path, with_ca: bool, system: bool) -> None:
    # A server certificate from a public CA cannot be made in a test, so the
    # pinning is checked on the transport's arguments: a given bundle is the
    # only trust, not an addition to the system's.
    ca = Authority()
    f = write(tmp_path, ca.pem, ca.issue(client=True, ips=False))
    args, _ = (_tls(f) if with_ca else TLS())._read()
    assert args["tls_include_system_certs"] is system


class _Counting:
    """A caller's own transport around the SDK's rotating one: it sees every
    request, as a circuit breaker or a metrics wrapper would."""

    def __init__(self, inner: Any) -> None:
        self.inner, self.requests = inner, 0

    def execute_sync(self, request: Any) -> Any:
        self.requests += 1
        return self.inner.execute_sync(request)


def test_a_wrapped_rotating_transport_sees_every_request_and_the_rotation(
    serve: Any, tmp_path: Path
) -> None:
    from paladin import Client
    from paladin.iam.v1.health_service_connect import HealthServiceClientSync

    ca = Authority()
    first, second = ca.issue(client=True, ips=False), ca.issue(client=True, ips=False)
    f = write(tmp_path, ca.pem, first)
    srv = serve(ca, ca.issue(client=False))
    wrapper = _Counting(_tls(f, reload_interval=RELOAD_NOW).sync_transport())
    client = Client(srv.url)
    health = HealthServiceClientSync(
        client.base_url,
        interceptors=client.interceptors(),
        http_client=client.http_client(transport=wrapper),
    )
    health.get_version(health_service_pb2.GetVersionRequest())
    f.write_client(second)
    _touch_later(f.cert, f.key)
    health.get_version(health_service_pb2.GetVersionRequest())
    assert srv.serials == [first.serial, second.serial]
    assert wrapper.requests == len(srv.serials)
