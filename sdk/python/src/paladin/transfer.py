"""How ``upload`` and ``download`` reach storage: the presigned requests.

Those requests go to the storage backend, not to Paladin, so the client's
credentials, retries and interceptors are not on them. They go through a
``Transfer``, declared once on ``connect``; a data plane without one shares a
default.
"""

from __future__ import annotations

import base64
import hashlib
import io
import threading
import time
from collections.abc import AsyncIterable, AsyncIterator, Callable, Iterable, Iterator
from contextlib import (
    AbstractAsyncContextManager,
    AbstractContextManager,
    asynccontextmanager,
    contextmanager,
)
from typing import TYPE_CHECKING, Any
from urllib.parse import SplitResult, urlsplit, urlunsplit

import pyqwest

from paladin.observe import Hooks, TransferEvent, report_transfer
from paladin.tls import TLS

if TYPE_CHECKING:  # annotations only: typing.Self is 3.11+
    from typing_extensions import Self

DEFAULT_TRANSFER_CONNECT_TIMEOUT = 10.0
"""Seconds to open a connection to storage."""
DEFAULT_TRANSFER_READ_TIMEOUT = 30.0
"""Seconds storage may go silent mid-response. There is no bound on a whole
transfer: a large object streams for as long as it takes."""
DEFAULT_TRANSFER_POOL_MAX_IDLE_PER_HOST = 32
"""Connections to one storage host kept for reuse, for many concurrent transfers."""

ERROR_BODY_LIMIT = 512
"""Bytes of a refused transfer's body an error quotes."""

CHECKSUM_SHA256 = "SHA256"
CHECKSUM_CRC32C = "CRC32C"
CHECKSUM_MD5 = "MD5"
"""The algorithms the server records an object's checksum under."""

_HEADER_HOST = "Host"
_URL_SCHEMES = ("http", "https")
_STATUS_OK = 200
_STATUS_REDIRECT = 300
_STATUS_PARTIAL_CONTENT = 206
_INTEGRITY_SIZE = "size"
"""``IntegrityError.what`` for a length mismatch."""
_ROOT_PATHS = ("", "/")


class TransferError(Exception):
    """A presigned request that storage refused, or answered with a redirect:
    a presigned URL never redirects legitimately, and following one would send
    its signed headers to another host. ``host`` is the host the request went
    to; the URL is not kept, because its query is the signature."""

    def __init__(self, method: str, host: str, status: int, body: str) -> None:
        self.method, self.host, self.status, self.body = method, host, status, body
        super().__init__(f"storage refused {method} {host}: {status} {body}")


class RangeIgnoredError(Exception):
    """Storage answered a range request with the whole object."""


class IntegrityError(Exception):
    """Downloaded content that does not match what the server recorded for the
    object: ``what`` is ``"size"`` or the checksum algorithm."""

    def __init__(self, what: str, want: str, got: str) -> None:
        self.what, self.want, self.got = what, want, got
        super().__init__(
            f"downloaded content does not match the object's {what}: want {want}, got {got}"
        )


def _crc32c() -> Any:
    try:
        import google_crc32c  # optional: the "crc32c" extra
    except ImportError:
        return None
    return google_crc32c.Checksum()


# A new digest per algorithm; None when this environment cannot compute it.
_DIGESTS: dict[str, Callable[[], Any]] = {
    CHECKSUM_SHA256: hashlib.sha256,
    CHECKSUM_MD5: lambda: hashlib.md5(usedforsecurity=False),
    CHECKSUM_CRC32C: _crc32c,
}


def _origin(value: str) -> SplitResult:
    parts = urlsplit(value)
    if (
        parts.scheme not in _URL_SCHEMES
        or not parts.netloc
        or parts.path not in _ROOT_PATHS
        or parts.query
    ):
        raise ValueError(f"an origin must be an absolute http or https URL with no path: {value!r}")
    return parts


class Transfer:
    """Sends the presigned requests of ``upload`` and ``download``.

    With no arguments: a connection timeout and a read timeout, but no bound on
    a whole transfer; connections kept for reuse; redirects refused.

    ``split_horizon=(signed_origin, internal_origin)`` sends a URL signed for
    ``signed_origin`` to ``internal_origin``, keeping the signed ``Host``
    header: the signature covers the header, not the address. Both are
    ``scheme://host[:port]``; other origins go as signed. ``rewrite`` is the
    general form, any URL to any URL, the signed Host still kept.

    ``hooks`` are told as each request ends (``Hooks.on_transfer``), and
    the ``paladin`` logger records it. ``otel=True`` turns on the HTTP
    stack's own OpenTelemetry spans for these requests, with W3C trace
    context, through ``tracer_provider`` or the global one.

    ``tls`` makes the connections to storage with a CA bundle and a client
    certificate that may rotate on disk (see ``TLS``).

    ``transport`` and ``async_transport`` replace the ``pyqwest`` transports
    built here, for the sync and the async workflows — for a proxy or other
    settings. Build it with ``follow_redirects=False``: a
    transport that follows them cannot be stopped from here.

    Safe to share between threads, and meant to be shared: its connection
    pool is what makes many concurrent transfers to one host cheap.
    """

    def __init__(
        self,
        *,
        split_horizon: tuple[str, str] | None = None,
        rewrite: Callable[[str], str] | None = None,
        connect_timeout: float = DEFAULT_TRANSFER_CONNECT_TIMEOUT,
        read_timeout: float = DEFAULT_TRANSFER_READ_TIMEOUT,
        pool_max_idle_per_host: int = DEFAULT_TRANSFER_POOL_MAX_IDLE_PER_HOST,
        tls: TLS | None = None,
        hooks: Hooks | None = None,
        otel: bool = False,
        tracer_provider: Any = None,
        transport: Any = None,
        async_transport: Any = None,
    ) -> None:
        if split_horizon is not None and rewrite is not None:
            raise ValueError("give split_horizon or rewrite, not both")
        if split_horizon is not None:
            rewrite = _split_horizon(*split_horizon)
        self._rewrite = rewrite
        self.hooks = hooks
        if tls is not None and transport is not None:
            raise ValueError("tls builds the transport; give one or the other")
        settings: dict[str, Any] = {
            "connect_timeout": connect_timeout,
            "read_timeout": read_timeout,
            "pool_max_idle_per_host": pool_max_idle_per_host,
            "follow_redirects": False,
            "enable_otel": otel,
            "tracer_provider": tracer_provider,
        }
        if transport is None:
            transport = (
                tls.sync_transport(**settings)
                if tls is not None
                else pyqwest.SyncHTTPTransport(**settings)
            )
        if async_transport is None:
            async_transport = (
                tls.async_transport(**settings)
                if tls is not None
                else pyqwest.HTTPTransport(**settings)
            )
        self._client = pyqwest.SyncClient(transport)
        self._async_client = pyqwest.Client(async_transport)

    def _target(self, url: str) -> tuple[str, str]:
        """The URL to send to, and the Host header it was signed for."""
        host = urlsplit(url).netloc
        return (self._rewrite(url) if self._rewrite else url), host

    @contextmanager
    def stream(
        self,
        method: str,
        signed: Any,
        headers: dict[str, str] | None = None,
        content: bytes | Iterable[bytes] | None = None,
    ) -> Iterator[Any]:
        """Send one presigned request and yield storage's 2xx response,
        unread; any other status is a ``TransferError``."""
        method = signed.method or method
        url, host = self._target(signed.url)
        sent = dict(headers or {})
        sent[_HEADER_HOST] = host
        # Covered by the signature: storage refuses the request without them.
        sent.update(signed.required_headers)
        started = time.monotonic()
        target = urlsplit(url).netloc
        try:
            with self._client.stream(method, url, sent, content) as resp:
                if not _STATUS_OK <= resp.status < _STATUS_REDIRECT:
                    raise TransferError(method, target, resp.status, _excerpt(resp))
                yield resp
        except Exception as err:
            self.ended(method, target, 0, started, err)
            raise

    @asynccontextmanager
    async def astream(
        self,
        method: str,
        signed: Any,
        headers: dict[str, str] | None = None,
        content: bytes | AsyncIterable[bytes] | None = None,
    ) -> AsyncIterator[Any]:
        """``stream`` for the async workflows."""
        method = signed.method or method
        url, host = self._target(signed.url)
        sent = dict(headers or {})
        sent[_HEADER_HOST] = host
        sent.update(signed.required_headers)
        started = time.monotonic()
        target = urlsplit(url).netloc
        try:
            async with self._async_client.stream(method, url, sent, content) as resp:
                if not _STATUS_OK <= resp.status < _STATUS_REDIRECT:
                    raise TransferError(method, target, resp.status, await _aexcerpt(resp))
                yield resp
        except Exception as err:
            self.ended(method, target, 0, started, err)
            raise

    def host_of(self, signed: Any) -> str:
        """The host a request for ``signed`` goes to, after any rewrite."""
        return urlsplit(self._target(signed.url)[0]).netloc

    def ended(
        self, method: str, host: str, moved: int, started: float, error: BaseException | None
    ) -> None:
        """Report a request that ended to the hooks and the logger."""
        report_transfer(
            self.hooks, TransferEvent(method, host, moved, time.monotonic() - started, error)
        )


def _split_horizon(signed_origin: str, internal_origin: str) -> Callable[[str], str]:
    signed, internal = _origin(signed_origin), _origin(internal_origin)

    def rewrite(url: str) -> str:
        parts = urlsplit(url)
        if (parts.scheme, parts.netloc) != (signed.scheme, signed.netloc):
            return url
        return urlunsplit(parts._replace(scheme=internal.scheme, netloc=internal.netloc))

    return rewrite


async def _aexcerpt(resp: Any) -> str:
    got = bytearray()
    async for chunk in resp.content:
        got += chunk
        if len(got) >= ERROR_BODY_LIMIT:
            break
    return bytes(got[:ERROR_BODY_LIMIT]).decode(errors="replace")


def _excerpt(resp: Any) -> str:
    got = bytearray()
    for chunk in resp.content:
        got += chunk
        if len(got) >= ERROR_BODY_LIMIT:
            break
    return bytes(got[:ERROR_BODY_LIMIT]).decode(errors="replace")


_default: Transfer | None = None
_default_lock = threading.Lock()


def default_transfer() -> Transfer:
    """The Transfer of a data plane built without one, shared by all of them."""
    global _default
    with _default_lock:
        if _default is None:
            _default = Transfer()
        return _default


class _Check:
    """What a whole read must match: the object's recorded size and, when the
    server recorded one this environment can compute, its checksum. Fed the
    content as it is read; ``finish`` raises ``IntegrityError`` on a mismatch."""

    def __init__(self, obj: Any, *, verify: bool) -> None:
        self.read = 0
        self._want_size: int | None = None
        self._digest: Any = None
        self._want_digest = b""
        self._algorithm = ""
        if verify:
            self._expect(obj)

    def _expect(self, obj: Any) -> None:
        if obj.size_bytes > 0:
            self._want_size = obj.size_bytes
        new = _DIGESTS.get(obj.checksum.algorithm)
        digest = new() if new else None
        if digest is None:
            return
        try:
            want = base64.b64decode(obj.checksum.value, validate=True)
        except ValueError:
            return
        # A multipart object's checksum is a composite of its parts': it does
        # not decode to a digest of the algorithm's length, and is not checked.
        if len(want) != len(digest.digest()):
            return
        self._digest, self._want_digest, self._algorithm = digest, want, obj.checksum.algorithm

    def update(self, piece: bytes) -> None:
        # bytes, not a view: google_crc32c accepts nothing else.
        if self._digest is not None:
            self._digest.update(piece)
        self.read += len(piece)

    def finish(self) -> None:
        if self._want_size is not None and self.read != self._want_size:
            raise IntegrityError(_INTEGRITY_SIZE, str(self._want_size), str(self.read))
        if self._digest is not None:
            got = self._digest.digest()
            if got != self._want_digest:
                raise IntegrityError(
                    self._algorithm,
                    base64.b64encode(self._want_digest).decode(),
                    base64.b64encode(got).decode(),
                )


class _Report:
    """Reports a download once: at the end of the content, or at close."""

    def __init__(self, ended: Callable[[int, BaseException | None], None] | None) -> None:
        self._ended = ended
        self._done = False

    def __call__(self, moved: int, error: BaseException | None) -> None:
        if not self._done and self._ended is not None:
            self._done = True
            self._ended(moved, error)


class ObjectReader(io.RawIOBase):
    """An object's content, streamed. A file-like object: ``read``,
    ``readinto``, iteration over ``chunks()``; close it, or use it in a
    ``with`` block.

    Reading a whole object verifies it at the end: the read that reaches the
    end raises ``IntegrityError`` when the size or the recorded checksum does
    not match. A range is not verified — the checksum covers the whole object.
    """

    def __init__(
        self,
        exchange: AbstractContextManager[Any],
        resp: Any,
        obj: Any,
        *,
        verify: bool,
        ended: Callable[[int, BaseException | None], None] | None = None,
    ) -> None:
        super().__init__()
        self._report = _Report(ended)
        self._cm = exchange
        self.object = obj
        self.content_type: str = resp.headers.get("content-type") or obj.content_type
        length = resp.headers.get("content-length")
        self.content_length: int | None = int(length) if length is not None else None
        self._chunks = iter(resp.content)
        self._pending = b""
        self._offset = 0
        self._check = _Check(obj, verify=verify)

    def readable(self) -> bool:
        return True

    def readinto(self, buffer: Any) -> int:
        while self._offset >= len(self._pending):
            try:
                self._pending, self._offset = bytes(next(self._chunks)), 0
            except StopIteration:
                try:
                    self._check.finish()
                except IntegrityError as err:
                    self._report(self._check.read, err)
                    raise
                self._report(self._check.read, None)
                return 0
        n = min(len(buffer), len(self._pending) - self._offset)
        piece = self._pending[self._offset : self._offset + n]
        self._offset += n
        buffer[:n] = piece
        self._check.update(piece)
        return n

    def chunks(self) -> Iterator[bytes]:
        """The content in the pieces storage sent, verified like ``read``."""
        while chunk := self.read(io.DEFAULT_BUFFER_SIZE):
            yield chunk

    def close(self) -> None:
        if not self.closed:
            self._report(self._check.read, None)
            self._cm.__exit__(None, None, None)
        super().close()


class AsyncObjectReader:
    """``ObjectReader`` for asyncio: ``await read()``, ``async for`` over
    ``chunks()``, ``await aclose()`` or ``async with``. Verified the same way."""

    def __init__(
        self,
        exchange: AbstractAsyncContextManager[Any],
        resp: Any,
        obj: Any,
        *,
        verify: bool,
        ended: Callable[[int, BaseException | None], None] | None = None,
    ) -> None:
        self._report = _Report(ended)
        self._cm = exchange
        self.object = obj
        self.content_type: str = resp.headers.get("content-type") or obj.content_type
        length = resp.headers.get("content-length")
        self.content_length: int | None = int(length) if length is not None else None
        self._chunks = aiter(resp.content)
        self._check = _Check(obj, verify=verify)
        self._closed = False

    async def chunks(self) -> AsyncIterator[bytes]:
        """The content in the pieces storage sent; the last is verified."""
        while True:
            try:
                piece = bytes(await anext(self._chunks))
            except StopAsyncIteration:
                try:
                    self._check.finish()
                except IntegrityError as err:
                    self._report(self._check.read, err)
                    raise
                self._report(self._check.read, None)
                return
            self._check.update(piece)
            yield piece

    async def read(self) -> bytes:
        """The rest of the content, verified."""
        got = bytearray()
        async for piece in self.chunks():
            got += piece
        return bytes(got)

    async def aclose(self) -> None:
        if not self._closed:
            self._closed = True
            self._report(self._check.read, None)
            await self._cm.__aexit__(None, None, None)

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()


def range_header(offset: int, length: int) -> str:
    """``bytes=first-last``, inclusive; ``length`` 0 reads to the end."""
    if length == 0:
        return f"bytes={offset}-"
    return f"bytes={offset}-{offset + length - 1}"


def require_partial(resp: Any) -> None:
    if resp.status != _STATUS_PARTIAL_CONTENT:
        raise RangeIgnoredError("storage ignored the range and sent the whole object")
