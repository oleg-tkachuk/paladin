"""A mutual-TLS test server over HTTP/1.1 with keep-alive, or HTTP/2.

It answers every request 200 with an empty body — which is an empty
``VersionInfo`` to a Connect unary call, and a stored object to a presigned
PUT — and records, per request, the connection it came on and the serial of
the client certificate that connection was made with. It speaks HTTP through
h11 and h2, the libraries httpcore itself is built on.
"""

from __future__ import annotations

import socket
import ssl
import threading
from dataclasses import dataclass, field
from pathlib import Path

import h2.config
import h2.connection
import h2.events
import h11
from certs import Authority, Leaf
from cryptography import x509

_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_BACKLOG = 16
_READ_SIZE = 65536
_H2 = "h2"
_HTTP1 = "http/1.1"
_OK = 200
# What a Connect unary call and a presigned PUT both accept.
_CONTENT_TYPE = "application/proto"
_ETAG = '"e"'


@dataclass(frozen=True)
class Seen:
    """One request: the connection it came on and that connection's client
    certificate serial."""

    conn: int
    serial: int | None


@dataclass
class TLSServer:
    url: str = ""
    seen: list[Seen] = field(default_factory=list)
    closed: set[int] = field(default_factory=set)
    protocols: list[str] = field(default_factory=list)
    _lock: threading.Lock = field(default_factory=threading.Lock)
    _stop: threading.Event = field(default_factory=threading.Event)
    _sock: socket.socket | None = None

    def serials(self) -> list[int | None]:
        with self._lock:
            return [s.serial for s in self.seen]

    def conns(self) -> list[int]:
        with self._lock:
            return [s.conn for s in self.seen]

    def is_closed(self, conn: int) -> bool:
        with self._lock:
            return conn in self.closed

    def stop(self) -> None:
        self._stop.set()
        if self._sock is not None:
            self._sock.close()


def serve(
    tmp: Path,
    ca: Authority,
    server: Leaf,
    *,
    h2: bool,
    max_version: ssl.TLSVersion | None = None,
    silent: bool = False,
) -> TLSServer:
    """Start a server presenting ``server``, requiring a client certificate
    from ``ca``; ``h2`` offers HTTP/2, ``max_version`` caps the protocol, and
    ``silent`` reads requests but never answers them."""
    cert, key, ca_file = (
        tmp / f"s{server.serial}.crt",
        tmp / f"s{server.serial}.key",
        tmp / "sca.pem",
    )
    cert.write_bytes(server.cert_pem)
    key.write_bytes(server.key_pem)
    ca_file.write_bytes(ca.pem)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert, key)
    context.load_verify_locations(ca_file)
    context.verify_mode = ssl.CERT_REQUIRED
    if max_version is not None:
        context.maximum_version = max_version
    context.set_alpn_protocols([_H2, _HTTP1] if h2 else [_HTTP1])

    srv = TLSServer()
    sock = socket.create_server((_LOOPBACK, _EPHEMERAL_PORT), backlog=_BACKLOG)
    srv._sock = sock
    srv.url = f"https://{_LOOPBACK}:{sock.getsockname()[1]}"
    counter = iter(range(1, 1 << 30))

    def accept() -> None:
        while not srv._stop.is_set():
            try:
                raw, _ = sock.accept()
            except OSError:
                return
            threading.Thread(target=handle, args=(raw, next(counter)), daemon=True).start()

    def handle(raw: socket.socket, conn: int) -> None:
        try:
            tls = context.wrap_socket(raw, server_side=True)
        except (ssl.SSLError, OSError):
            raw.close()
            return
        der = tls.getpeercert(binary_form=True)
        serial = x509.load_der_x509_certificate(der).serial_number if der else None
        proto = tls.selected_alpn_protocol() or _HTTP1
        with srv._lock:
            srv.protocols.append(proto)

        def record() -> None:
            with srv._lock:
                srv.seen.append(Seen(conn, serial))

        try:
            if silent:
                while tls.recv(_READ_SIZE) and not srv._stop.is_set():
                    pass
                return
            (_serve_h2 if proto == _H2 else _serve_h1)(tls, record)
        except (OSError, ssl.SSLError, h11.RemoteProtocolError):
            pass
        finally:
            tls.close()
            with srv._lock:
                srv.closed.add(conn)

    threading.Thread(target=accept, daemon=True).start()
    return srv


def _serve_h1(tls: ssl.SSLSocket, record: object) -> None:
    conn = h11.Connection(h11.SERVER)
    while True:
        event = conn.next_event()
        if event is h11.NEED_DATA:
            data = tls.recv(_READ_SIZE)
            conn.receive_data(data)
            if not data:
                return
            continue
        if isinstance(event, h11.ConnectionClosed):
            return
        if isinstance(event, h11.EndOfMessage):
            record()  # type: ignore[operator]
            headers = [("content-type", _CONTENT_TYPE), ("etag", _ETAG), ("content-length", "0")]
            tls.sendall(conn.send(h11.Response(status_code=_OK, headers=headers)))
            tls.sendall(conn.send(h11.EndOfMessage()))
            conn.start_next_cycle()


def _serve_h2(tls: ssl.SSLSocket, record: object) -> None:
    conn = h2.connection.H2Connection(h2.config.H2Configuration(client_side=False))
    conn.initiate_connection()
    tls.sendall(conn.data_to_send())
    while True:
        data = tls.recv(_READ_SIZE)
        if not data:
            return
        for event in conn.receive_data(data):
            if isinstance(event, h2.events.DataReceived):
                conn.acknowledge_received_data(event.flow_controlled_length, event.stream_id)
            elif isinstance(event, h2.events.StreamEnded):
                record()  # type: ignore[operator]
                conn.send_headers(
                    event.stream_id,
                    [(":status", str(_OK)), ("content-type", _CONTENT_TYPE), ("etag", _ETAG)],
                    end_stream=True,
                )
            elif isinstance(event, h2.events.ConnectionTerminated):
                tls.sendall(conn.data_to_send())
                return
        tls.sendall(conn.data_to_send())
