"""The HTTP stack under ``TLS``: pyqwest transports over httpcore and ``ssl``.

pyqwest, the HTTP stack connect-python runs on, verifies a server's
certificate inside Rust with no hook into it, and takes no protocol floor —
so ``TLS(server_id=…)``, ``verify_peer`` and ``min_version`` cannot be had
from it. These transports implement pyqwest's ``SyncTransport`` and
``Transport`` protocols on httpcore instead, with the standard library's
``ssl`` making the connection:

* The certificate chain is verified by OpenSSL against the CA bundle. With a
  ``server_id`` the host name is not checked; the leaf is then verified as an
  X.509-SVID for that ID.
* The check runs in the network stream's ``start_tls``, after the handshake
  and before httpcore writes anything: httpcore sends no request bytes, not
  even the HTTP/2 preface, until ``start_tls`` returns. A refused peer gets
  the TLS handshake and a close, never a request — so neither a token nor a
  body reaches a workload the caller did not mean.
* Each load of the files gets a connection pool of its own. Once the files
  change, every new request goes to the new pool; the old one finishes the
  requests it carries and is then closed, so no request is sent on a
  connection made with replaced files.

Plaintext connections stay on pyqwest. A response's ``Content-Encoding`` is
decoded here, as pyqwest does, because connect-python leaves unary
decompression to the HTTP stack.
"""

from __future__ import annotations

import contextlib
import ssl
import threading
import zlib
from collections.abc import AsyncIterator, Callable, Iterator
from contextvars import ContextVar
from dataclasses import dataclass, field
from time import monotonic
from typing import Any
from urllib.parse import urlsplit

import httpcore
import pyqwest

# How the HTTP stack names the protocol a response came over.
_HTTP2 = b"HTTP/2"
# Header names this module reads or writes.
_HOST = "host"
_CONTENT_ENCODING = "content-encoding"
_CONTENT_LENGTH = "content-length"
_TRANSFER_ENCODING = "transfer-encoding"
_CHUNKED = "chunked"
# Methods that carry no body, so no Content-Length: 0 is sent with them.
_BODILESS = frozenset({"GET", "HEAD", "DELETE", "OPTIONS"})
# Response encodings decoded here: gzip and deflate by the standard library,
# br and zstd by connect-python's own codecs when their packages are present.
_GZIP = "gzip"
_DEFLATE = "deflate"
_IDENTITY = "identity"
_GZIP_WBITS = 16 + zlib.MAX_WBITS
# httpcore's ssl_object attribute on a network stream.
_SSL_OBJECT = "ssl_object"
# The OpenTelemetry instrumentation scope of the spans made here.
_TRACER_NAME = "paladin.tls"

# The deadline of the RPC being sent, as time.monotonic(); set by the SDK's
# interceptors, which see the call's timeout. pyqwest hands a sync transport
# the timeout only through a private module, so it is carried here instead.
call_deadline: ContextVar[float | None] = ContextVar("paladin_call_deadline", default=None)


@dataclass(frozen=True)
class Settings:
    """The transport settings ``TLS`` honours; pyqwest's names, so that a
    caller's ``Transfer`` settings mean the same over TLS."""

    connect_timeout: float | None = None
    read_timeout: float | None = None
    pool_idle_timeout: float | None = None
    pool_max_idle_per_host: int | None = None
    enable_otel: bool = True
    tracer_provider: Any = None
    # pyqwest follows redirects by default; httpcore never does.
    follow_redirects: bool = field(default=False)


# ─── Verification at the handshake ──────────────────────────────────────────


def _verified(stream: Any, verify: Callable[[ssl.SSLObject | ssl.SSLSocket], None]) -> Any:
    try:
        verify(stream.get_extra_info(_SSL_OBJECT))
    except BaseException:
        stream.close()
        raise
    return stream


class _VerifyingStream(httpcore.NetworkStream):
    def __init__(self, inner: httpcore.NetworkStream, verify: Any) -> None:
        self._inner, self._verify = inner, verify

    def read(self, max_bytes: int, timeout: float | None = None) -> bytes:
        return self._inner.read(max_bytes, timeout)

    def write(self, buffer: bytes, timeout: float | None = None) -> None:
        self._inner.write(buffer, timeout)

    def close(self) -> None:
        self._inner.close()

    def start_tls(
        self,
        ssl_context: ssl.SSLContext,
        server_hostname: str | None = None,
        timeout: float | None = None,
    ) -> httpcore.NetworkStream:
        return _verified(self._inner.start_tls(ssl_context, server_hostname, timeout), self._verify)

    def get_extra_info(self, info: str) -> Any:
        return self._inner.get_extra_info(info)


class _VerifyingBackend(httpcore.NetworkBackend):
    def __init__(self, verify: Any) -> None:
        self._inner, self._verify = httpcore.SyncBackend(), verify

    def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options: Any = None,
    ) -> httpcore.NetworkStream:
        stream = self._inner.connect_tcp(host, port, timeout, local_address, socket_options)
        return _VerifyingStream(stream, self._verify)

    def connect_unix_socket(
        self, path: str, timeout: float | None = None, socket_options: Any = None
    ) -> httpcore.NetworkStream:
        return self._inner.connect_unix_socket(path, timeout, socket_options)

    def sleep(self, seconds: float) -> None:
        self._inner.sleep(seconds)


class _AsyncVerifyingStream(httpcore.AsyncNetworkStream):
    def __init__(self, inner: httpcore.AsyncNetworkStream, verify: Any) -> None:
        self._inner, self._verify = inner, verify

    async def read(self, max_bytes: int, timeout: float | None = None) -> bytes:
        return await self._inner.read(max_bytes, timeout)

    async def write(self, buffer: bytes, timeout: float | None = None) -> None:
        await self._inner.write(buffer, timeout)

    async def aclose(self) -> None:
        await self._inner.aclose()

    async def start_tls(
        self,
        ssl_context: ssl.SSLContext,
        server_hostname: str | None = None,
        timeout: float | None = None,
    ) -> httpcore.AsyncNetworkStream:
        stream = await self._inner.start_tls(ssl_context, server_hostname, timeout)
        try:
            self._verify(stream.get_extra_info(_SSL_OBJECT))
        except BaseException:
            await stream.aclose()
            raise
        return stream

    def get_extra_info(self, info: str) -> Any:
        return self._inner.get_extra_info(info)


class _AsyncVerifyingBackend(httpcore.AsyncNetworkBackend):
    def __init__(self, verify: Any) -> None:
        self._inner, self._verify = httpcore.AnyIOBackend(), verify

    async def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options: Any = None,
    ) -> httpcore.AsyncNetworkStream:
        stream = await self._inner.connect_tcp(host, port, timeout, local_address, socket_options)
        return _AsyncVerifyingStream(stream, self._verify)

    async def connect_unix_socket(
        self, path: str, timeout: float | None = None, socket_options: Any = None
    ) -> httpcore.AsyncNetworkStream:
        return await self._inner.connect_unix_socket(path, timeout, socket_options)

    async def sleep(self, seconds: float) -> None:
        await self._inner.sleep(seconds)


# ─── Requests and responses ─────────────────────────────────────────────────


def _timeouts(settings: Settings) -> dict[str, float | None]:
    """httpcore's per-phase timeouts, capped by the call's deadline."""
    read = settings.read_timeout
    connect = settings.connect_timeout
    deadline = call_deadline.get()
    if deadline is not None:
        left = max(deadline - monotonic(), 0.0)
        read = left if read is None else min(read, left)
        connect = left if connect is None else min(connect, left)
    return {"connect": connect, "read": read, "write": read, "pool": connect}


def _request(request: Any, settings: Settings, content: Any) -> httpcore.Request:
    headers = [(k, v) for k, v in request.headers.items()]
    names = {k.lower() for k, _ in headers}
    if _HOST not in names:
        # HTTP/1.1 needs it, and httpcore adds none; HTTP/2 takes :authority
        # from it.
        headers.insert(0, (_HOST, urlsplit(request.url).netloc))
    # The framing pyqwest adds itself and httpcore does not; on HTTP/2
    # httpcore reads it to tell whether a body follows.
    if _CONTENT_LENGTH not in names and _TRANSFER_ENCODING not in names:
        if isinstance(content, (bytes, bytearray, memoryview)):
            if content or request.method not in _BODILESS:
                headers.append((_CONTENT_LENGTH, str(len(content))))
        elif content is not None:
            headers.append((_TRANSFER_ENCODING, _CHUNKED))
    return httpcore.Request(
        request.method,
        request.url,
        headers=headers,
        content=content,
        extensions={"timeout": _timeouts(settings)},
    )


def _decoder(encoding: str) -> Callable[[bytes, bool], bytes] | None:
    """A decoder for a Content-Encoding, fed chunk by chunk; None when the
    body is to pass as it is."""
    if encoding in ("", _IDENTITY):
        return None
    if encoding in (_GZIP, _DEFLATE):
        state = zlib.decompressobj(_GZIP_WBITS if encoding == _GZIP else zlib.MAX_WBITS)

        def stream(chunk: bytes, last: bool) -> bytes:
            return state.decompress(chunk) + (state.flush() if last else b"")

        return stream
    codec = _connect_codec(encoding)
    held = bytearray()

    def whole(chunk: bytes, last: bool) -> bytes:
        held.extend(chunk)
        return codec.decompress(bytes(held)) if last else b""

    return whole


def _connect_codec(encoding: str) -> Any:
    """connect-python's codec for ``br`` or ``zstd``, which it offers the
    server only when the codec's package is installed."""
    if encoding == "br":
        from connectrpc.compression.brotli import BrotliCompression

        return BrotliCompression()
    if encoding == "zstd":
        from connectrpc.compression.zstd import ZstdCompression

        return ZstdCompression()
    raise pyqwest.ReadError(f"paladin: unsupported Content-Encoding {encoding!r}")


def _response_parts(resp: httpcore.Response) -> tuple[Any, pyqwest.Headers, Any]:
    decode = None
    headers = pyqwest.Headers()
    for name, value in resp.headers:
        key, val = name.decode("latin-1"), value.decode("latin-1")
        if key.lower() == _CONTENT_ENCODING:
            decode = _decoder(val.strip().lower())
            if decode is not None:
                continue
        headers.add(key, val)
    if decode is not None:
        headers.pop(_CONTENT_LENGTH, None)
    version = (
        pyqwest.HTTPVersion.HTTP2
        if resp.extensions.get("http_version") == _HTTP2
        else pyqwest.HTTPVersion.HTTP1
    )
    return version, headers, decode


# ─── Errors, as pyqwest's own transports raise them ─────────────────────────

# httpcore's errors as pyqwest raises them, so a caller's except clauses — and
# connect-python's, which turns a TimeoutError into DEADLINE_EXCEEDED — read
# the same over TLS as without it. Order matters: the first match wins.
_ERRORS: tuple[tuple[type[Exception], type[Exception]], ...] = (
    (httpcore.ConnectTimeout, pyqwest.ConnectTimeout),
    (httpcore.TimeoutException, TimeoutError),
    (httpcore.ConnectError, ConnectionError),
    (httpcore.ReadError, pyqwest.ReadError),
    (httpcore.WriteError, pyqwest.WriteError),
    (httpcore.ProtocolError, pyqwest.RemoteProtocolError),
)


@contextlib.contextmanager
def _pyqwest_errors() -> Iterator[None]:
    try:
        yield
    except httpcore.NetworkError as err:
        raise _translated(err) from err
    except (httpcore.TimeoutException, httpcore.ProtocolError) as err:
        raise _translated(err) from err


def _translated(err: Exception) -> Exception:
    for theirs, ours in _ERRORS:
        if isinstance(err, theirs):
            return ours(str(err))
    return err


# ─── OpenTelemetry, as pyqwest's own transports make it ─────────────────────


@contextlib.contextmanager
def _span(settings: Settings, request: Any) -> Iterator[Any]:
    if not settings.enable_otel:
        yield None
        return
    try:
        from opentelemetry import propagate, trace
    except ImportError:  # not installed: nothing to report to
        yield None
        return
    provider = settings.tracer_provider or trace.get_tracer_provider()
    tracer = provider.get_tracer(_TRACER_NAME)
    url = urlsplit(request.url)
    with tracer.start_as_current_span(
        request.method,
        kind=trace.SpanKind.CLIENT,
        attributes={
            "http.request.method": request.method,
            # Without the query: a presigned URL's query is its signature.
            "url.full": f"{url.scheme}://{url.netloc}{url.path}",
            "server.address": url.hostname or "",
        },
    ) as span:
        propagate.inject(request.headers)
        yield span


# ─── Generations: one connection pool per load of the files ────────────────


@dataclass(eq=False)
class _Generation:
    key: tuple[int, ...]
    pool: Any
    in_flight: int = 0
    replaced: bool = False


class _Generations:
    """The pool for the files as they are now, and those still finishing
    requests after a rotation replaced them."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._current: _Generation | None = None
        self._draining: list[_Generation] = []

    def acquire(self, key: tuple[int, ...], make: Callable[[], Any]) -> tuple[_Generation, Any]:
        """The generation for files ``key``, counted in flight, and a replaced
        one now idle, to close."""
        with self._lock:
            idle = None
            if self._current is None or self._current.key != key:
                old = self._current
                self._current = _Generation(key, make())
                if old is not None:
                    old.replaced = True
                    if old.in_flight == 0:
                        idle = old
                    else:
                        self._draining.append(old)
            self._current.in_flight += 1
            return self._current, idle

    def release(self, g: _Generation) -> _Generation | None:
        """Ends a request on g; returns g when it is replaced and now idle."""
        with self._lock:
            g.in_flight -= 1
            if g.replaced and g.in_flight == 0 and g in self._draining:
                self._draining.remove(g)
                return g
            return None

    def all(self) -> list[_Generation]:
        with self._lock:
            current = [self._current] if self._current is not None else []
            gens = current + self._draining
            self._current, self._draining = None, []
            return gens


class SyncTLSTransport:
    """A ``pyqwest.SyncTransport`` over ``TLS``; see the module's doc."""

    def __init__(self, files: Any, settings: Settings) -> None:
        self._files, self._settings = files, settings
        self._gens = _Generations()

    def _pool(self, context: ssl.SSLContext) -> httpcore.ConnectionPool:
        return httpcore.ConnectionPool(
            ssl_context=context,
            http1=True,
            http2=True,
            max_keepalive_connections=self._settings.pool_max_idle_per_host,
            keepalive_expiry=self._settings.pool_idle_timeout,
            network_backend=_VerifyingBackend(self._files.verify),
        )

    def execute_sync(self, request: Any) -> pyqwest.SyncResponse:
        key, context = self._files.current()
        g, idle = self._gens.acquire(key, lambda: self._pool(context))
        if idle is not None:
            idle.pool.close()
        released = False

        def release() -> None:
            nonlocal released
            if not released:
                released = True
                done = self._gens.release(g)
                if done is not None:
                    done.pool.close()

        try:
            with _pyqwest_errors(), _span(self._settings, request) as span:
                resp = g.pool.handle_request(_request(request, self._settings, request.content))
                if span is not None:
                    span.set_attribute("http.response.status_code", resp.status)
        except BaseException:
            release()
            raise
        version, headers, decode = _response_parts(resp)

        def body() -> Iterator[bytes]:
            try:
                with _pyqwest_errors():
                    for chunk in resp.iter_stream():
                        yield decode(chunk, False) if decode else chunk
                if decode is not None:
                    yield decode(b"", True)
            finally:
                resp.close()
                release()

        return pyqwest.SyncResponse(
            status=resp.status, http_version=version, headers=headers, content=body()
        )

    def close(self) -> None:
        """Closes every pool; the transport is not to be used after."""
        for g in self._gens.all():
            g.pool.close()


class AsyncTLSTransport:
    """``SyncTLSTransport`` for the async clients."""

    def __init__(self, files: Any, settings: Settings) -> None:
        self._files, self._settings = files, settings
        self._gens = _Generations()

    def _pool(self, context: ssl.SSLContext) -> httpcore.AsyncConnectionPool:
        return httpcore.AsyncConnectionPool(
            ssl_context=context,
            http1=True,
            http2=True,
            max_keepalive_connections=self._settings.pool_max_idle_per_host,
            keepalive_expiry=self._settings.pool_idle_timeout,
            network_backend=_AsyncVerifyingBackend(self._files.verify),
        )

    async def execute(self, request: Any) -> pyqwest.Response:
        key, context = self._files.current()
        g, idle = self._gens.acquire(key, lambda: self._pool(context))
        if idle is not None:
            await idle.pool.aclose()
        released = False

        async def release() -> None:
            nonlocal released
            if not released:
                released = True
                done = self._gens.release(g)
                if done is not None:
                    await done.pool.aclose()

        content = request.content
        try:
            with _pyqwest_errors(), _span(self._settings, request) as span:
                resp = await g.pool.handle_async_request(_request(request, self._settings, content))
                if span is not None:
                    span.set_attribute("http.response.status_code", resp.status)
        except BaseException:
            await release()
            raise
        version, headers, decode = _response_parts(resp)

        async def body() -> AsyncIterator[bytes]:
            try:
                with _pyqwest_errors():
                    async for chunk in resp.aiter_stream():
                        yield decode(chunk, False) if decode else chunk
                if decode is not None:
                    yield decode(b"", True)
            finally:
                await resp.aclose()
                await release()

        return pyqwest.Response(
            status=resp.status, http_version=version, headers=headers, content=body()
        )

    async def aclose(self) -> None:
        """Closes every pool; the transport is not to be used after."""
        for g in self._gens.all():
            await g.pool.aclose()
