"""What takes more than one call: paging, waiting, masks, uploads, downloads."""

from __future__ import annotations

import asyncio
import threading
import time
import urllib.error
import urllib.request
from collections.abc import AsyncIterator, Awaitable, Callable, Iterator
from concurrent.futures import ThreadPoolExecutor
from typing import IO, Any, TypeVar

from connectrpc.code import Code
from google.protobuf import field_mask_pb2
from google.protobuf.message import Message
from google.rpc import code_pb2

from paladin.common.v1 import resource_pb2
from paladin.data.v1 import multipart_service_pb2, object_service_pb2, types_pb2
from paladin.facade import DataPlane

T = TypeVar("T")
M = TypeVar("M", bound=Message)

# Paging fields: every List request carries a PageRequest in `page`, and
# every response a PageResponse in `page`.
_PAGE = "page"

DEFAULT_POLL_INTERVAL = 0.5
"""Seconds before the first re-poll in ``wait``; the pause doubles from here."""
DEFAULT_MAX_POLL_INTERVAL = 10.0
"""Seconds the doubling pause in ``wait`` stops at."""

DEFAULT_MULTIPART_THRESHOLD = 8 << 20
"""Bytes above which ``upload`` goes multipart; up to it, one presigned PUT."""
DEFAULT_PART_CONCURRENCY = 3
"""Parts of a multipart upload in flight at once."""

_PUT = "PUT"
_GET = "GET"
_HEADER_CONTENT_TYPE = "Content-Type"
_HEADER_ETAG = "ETag"
_ETAG_QUOTE = '"'
# How much of a refused transfer's body an error quotes.
_ERROR_BODY_LIMIT = 512


def pages(call: Callable[[Any], Any], request: Message, items: str) -> Iterator[Any]:
    """Call a List RPC page by page and yield every item of the repeated field
    ``items``, following ``next_page_token`` until it is empty.

    ``for obj in pages(p.data.object.list_objects, ListObjectsRequest(parent=c), "objects"): …``
    """
    req = _copy(request)
    while True:
        resp = call(req)
        yield from getattr(resp, items)
        token = _next_token(resp)
        if not token:
            return
        req = _copy(req)
        _paged(req).page.page_token = token


async def apages(
    call: Callable[[Any], Awaitable[Any]], request: Message, items: str
) -> AsyncIterator[Any]:
    """``pages`` for the async clients."""
    req = _copy(request)
    while True:
        resp = await call(req)
        for item in getattr(resp, items):
            yield item
        token = _next_token(resp)
        if not token:
            return
        req = _copy(req)
        _paged(req).page.page_token = token


def _copy(message: M) -> M:
    out = type(message)()
    out.CopyFrom(message)
    return out


def _paged(message: Message) -> Any:
    if _PAGE not in message.DESCRIPTOR.fields_by_name:
        raise TypeError(f"{message.DESCRIPTOR.full_name} has no page field")
    return message


def _next_token(response: Message) -> str:
    return str(_paged(response).page.next_page_token)


class OperationFailed(Exception):
    """An operation that finished with an error."""

    def __init__(self, operation: Any) -> None:
        self.operation = operation
        self.code = _code(operation.error.code)
        super().__init__(
            f"operation {operation.name} failed: {self.code.name}: {operation.error.message}"
        )


# google.rpc spells one code differently from Connect.
_RPC_SPELLINGS = {"CANCELLED": "CANCELED"}


def _code(number: int) -> Code:
    """The Connect code for a google.rpc.Status code number."""
    try:
        name = code_pb2.Code.Name(number)
    except ValueError:
        return Code.UNKNOWN
    return Code.__members__.get(_RPC_SPELLINGS.get(name, name), Code.UNKNOWN)


def _finished(op: Any) -> bool:
    if not op.done:
        return False
    if op.HasField("error"):
        raise OperationFailed(op)
    return True


def wait(
    get: Callable[[], T],
    *,
    poll: float = DEFAULT_POLL_INTERVAL,
    max_poll: float = DEFAULT_MAX_POLL_INTERVAL,
    timeout: float | None = None,
) -> T:
    """Call ``get`` until the operation it returns is done, and return it.

    Raises ``OperationFailed`` for an operation that finished with an error,
    and ``TimeoutError`` when ``timeout`` seconds pass first.
    """
    deadline = None if timeout is None else time.monotonic() + timeout
    pause = poll
    while True:
        op = get()
        if _finished(op):
            return op
        if deadline is not None and time.monotonic() + pause > deadline:
            raise TimeoutError(f"operation {getattr(op, 'name', '')} still running")
        time.sleep(pause)
        pause = min(pause * 2, max_poll)


async def await_operation(
    get: Callable[[], Awaitable[T]],
    *,
    poll: float = DEFAULT_POLL_INTERVAL,
    max_poll: float = DEFAULT_MAX_POLL_INTERVAL,
) -> T:
    """``wait`` for the async clients; bound it with ``asyncio.timeout``."""
    pause = poll
    while True:
        op = await get()
        if _finished(op):
            return op
        await asyncio.sleep(pause)
        pause = min(pause * 2, max_poll)


def mask(message: type[Message], *paths: str) -> field_mask_pb2.FieldMask:
    """An update mask for ``message`` from proto field names, each checked
    against its descriptor: the server refuses a path it does not know.

    ``mask(EventSubscription, "filter", "sink.http.url")``
    """
    for path in paths:
        descriptor = message.DESCRIPTOR
        for name in path.split("."):
            if descriptor is None or name not in descriptor.fields_by_name:
                raise ValueError(f"{message.DESCRIPTOR.full_name} has no field {path!r}")
            descriptor = descriptor.fields_by_name[name].message_type
    return field_mask_pb2.FieldMask(paths=list(paths))


class TransferError(Exception):
    """A presigned request that storage refused."""

    def __init__(self, method: str, status: int, body: str) -> None:
        self.method, self.status, self.body = method, status, body
        super().__init__(f"storage refused {method}: {status} {body}")


def _send(url: Any, method: str, body: bytes | None, content_type: str = "") -> tuple[bytes, Any]:
    """Send one presigned request; return the body and the headers."""
    req = urllib.request.Request(url.url, data=body, method=url.method or method)
    if content_type:
        req.add_header(_HEADER_CONTENT_TYPE, content_type)
    # Covered by the signature: storage refuses the request without them.
    for name, value in url.required_headers.items():
        req.add_header(name, value)
    try:
        with urllib.request.urlopen(req) as resp:  # a URL the server presigned
            return resp.read(), resp.headers
    except urllib.error.HTTPError as err:
        raise TransferError(
            req.get_method(), err.code, err.read(_ERROR_BODY_LIMIT).decode(errors="replace")
        ) from err


def _etag(headers: Any) -> str:
    return str(headers.get(_HEADER_ETAG, "")).strip(_ETAG_QUOTE)


class _Body:
    """Reads ``length`` bytes at ``offset`` from bytes or a seekable file."""

    def __init__(self, body: bytes | IO[bytes]) -> None:
        self._body = body
        self._lock = threading.Lock()

    def read(self, offset: int, length: int) -> bytes:
        if isinstance(self._body, bytes | bytearray | memoryview):
            return bytes(self._body[offset : offset + length])
        with self._lock:
            self._body.seek(offset)
            return self._body.read(length)


def upload(
    data: DataPlane,
    *,
    parent: str,
    content_type: str,
    body: bytes | IO[bytes],
    size: int,
    key: str = "",
    metadata: dict[str, str] | None = None,
    tags: dict[str, str] | None = None,
    multipart_threshold: int = DEFAULT_MULTIPART_THRESHOLD,
    part_concurrency: int = DEFAULT_PART_CONCURRENCY,
) -> types_pb2.Object:
    """Store ``body`` (bytes, or a seekable binary file) as a new object and
    return it once complete: one presigned PUT up to ``multipart_threshold``
    bytes, multipart above it, whose failure aborts the session."""
    if size <= 0:
        raise ValueError("upload size must be positive")
    source = _Body(body)
    if size > multipart_threshold:
        return _upload_multipart(
            data, parent, key, content_type, size, source, metadata, tags, part_concurrency
        )
    allocated = data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            parent=parent,
            key=key,
            content_type=content_type,
            size_hint_bytes=size,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            metadata=metadata or {},
            tags=tags or {},
            transport=object_service_pb2.PRESIGN_TRANSPORT_PUT,
        )
    )
    if not allocated.upload_url.url:
        raise TransferError(_PUT, 0, "the server returned no upload URL")
    _, headers = _send(allocated.upload_url, _PUT, source.read(0, size), content_type)
    return data.object.complete_object(
        object_service_pb2.CompleteObjectRequest(name=allocated.object.name, etag=_etag(headers))
    )


def _upload_multipart(
    data: DataPlane,
    parent: str,
    key: str,
    content_type: str,
    size: int,
    source: _Body,
    metadata: dict[str, str] | None,
    tags: dict[str, str] | None,
    concurrency: int,
) -> types_pb2.Object:
    init = data.multipart_upload.initiate_multipart_upload(
        multipart_service_pb2.InitiateMultipartUploadRequest(
            parent=parent,
            key=key,
            content_type=content_type,
            size_bytes=size,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            metadata=metadata or {},
            tags=tags or {},
        )
    )
    name, upload_id = init.object.name, init.upload_id
    part_size = init.recommended_part_size or DEFAULT_MULTIPART_THRESHOLD
    count = -(-size // part_size)

    def send(index: int) -> types_pb2.CompletedPart:
        signed = data.multipart_upload.presign_part(
            multipart_service_pb2.PresignPartRequest(
                object_name=name, upload_id=upload_id, part_number=index + 1
            )
        )
        offset = index * part_size
        _, headers = _send(
            signed.upload_url, _PUT, source.read(offset, min(part_size, size - offset))
        )
        etag = _etag(headers)
        if not etag:
            raise TransferError(
                _PUT, 0, f"part {index + 1} returned no ETag; storage must expose it"
            )
        return types_pb2.CompletedPart(part_number=index + 1, etag=etag)

    try:
        with ThreadPoolExecutor(max_workers=max(1, min(concurrency, count))) as pool:
            parts = list(pool.map(send, range(count)))
        return data.multipart_upload.complete_multipart_upload(
            multipart_service_pb2.CompleteMultipartUploadRequest(
                object_name=name, upload_id=upload_id, parts=parts
            )
        )
    except BaseException:
        # Best effort: a session left open is swept by the server, and a
        # failed abort must not mask the error that ended the upload.
        try:
            data.multipart_upload.abort_multipart_upload(
                multipart_service_pb2.AbortMultipartUploadRequest(
                    object_name=name, upload_id=upload_id
                )
            )
        except Exception:  # noqa: BLE001, S110 -- see above
            pass
        raise


def download(data: DataPlane, name: str) -> bytes:
    """The object's content, fetched through a presigned URL."""
    resp = data.object.download_object(object_service_pb2.DownloadObjectRequest(name=name))
    if not resp.download_url.url:
        raise TransferError(_GET, 0, "the server returned no download URL")
    content, _ = _send(resp.download_url, _GET, None)
    return content
