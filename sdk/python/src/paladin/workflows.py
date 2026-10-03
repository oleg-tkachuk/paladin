"""What takes more than one call: paging, waiting, masks, uploads, downloads."""

from __future__ import annotations

import asyncio
import base64
import hashlib
import io
import time
from collections.abc import AsyncIterator, Awaitable, Callable, Iterable, Iterator
from concurrent.futures import FIRST_COMPLETED, Future, ThreadPoolExecutor
from concurrent.futures import wait as wait_futures
from dataclasses import dataclass
from typing import IO, Any, TypeVar

from connectrpc.code import Code
from google.protobuf import field_mask_pb2
from google.protobuf.message import Message
from google.rpc import code_pb2

from paladin._retry import already_stored, awith_retries, with_retries
from paladin.common.v1 import resource_pb2
from paladin.data.v1 import (
    multipart_service_pb2,
    object_service_pb2,
    presign_service_pb2,
    types_pb2,
)
from paladin.facade import AsyncDataPlane, DataPlane
from paladin.names import ObjectURI
from paladin.transfer import (
    AsyncObjectReader,
    ObjectReader,
    Transfer,
    TransferError,
    default_transfer,
    range_header,
    require_partial,
)

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
_HEADER_CONTENT_LENGTH = "Content-Length"
_HEADER_ETAG = "etag"
_HEADER_RANGE = "Range"
_ETAG_QUOTE = '"'
# How much of a stream one read takes while it is sent.
_STREAM_CHUNK = 64 << 10


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


def _transfer(data: DataPlane) -> Transfer:
    """The data plane's Transfer (``connect(transfer=…)``), else the default."""
    return getattr(data, "transfer", None) or default_transfer()


def _etag(headers: Any) -> str:
    return str(headers.get(_HEADER_ETAG) or "").strip(_ETAG_QUOTE)


def _reader(body: bytes | IO[bytes]) -> IO[bytes]:
    if isinstance(body, bytes | bytearray | memoryview):
        return io.BytesIO(body)
    return body


class ObjectChangedError(Exception):
    """A download whose object was replaced while it was being retried: the
    fresh URL is for different bytes than the first."""


@dataclass(frozen=True)
class UploadSession:
    """An open multipart upload: enough to resume it after a crash."""

    object_name: str
    upload_id: str
    part_size: int
    total_parts: int


# A part's ETag is the hex MD5 of its bytes when stored without KMS: 32 hex
# characters.
_PART_ETAG_LEN = 32


def _holds(stored: types_pb2.PartInfo | None, chunk: bytes) -> bool:
    """Whether a part storage reports is the one to be sent: the same length
    and, when its ETag is a plain MD5, the same bytes. An ETag of another form
    cannot be checked, so such a part is sent again — harmlessly."""
    if stored is None or stored.size_bytes != len(chunk) or len(stored.etag) != _PART_ETAG_LEN:
        return False
    return hashlib.md5(chunk, usedforsecurity=False).hexdigest() == stored.etag


def _sha256(chunk: bytes) -> str:
    """``chunk``'s SHA-256 as an upload's ``checksum_value``: base64 of the
    digest, as S3 writes it. The upload URL is signed for it."""
    return base64.b64encode(hashlib.sha256(chunk).digest()).decode()


def _read_exactly(reader: IO[bytes], length: int) -> bytes:
    got = bytearray()
    while len(got) < length:
        chunk = reader.read(length - len(got))
        if not chunk:
            raise ValueError(f"the upload body ended {length - len(got)} bytes short of its size")
        got += chunk
    return bytes(got)


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
    on_session: Callable[[UploadSession], None] | None = None,
    resume: UploadSession | None = None,
) -> types_pb2.Object:
    """Store ``size`` bytes of ``body`` as a new object and return it once
    complete. ``body`` is bytes or a binary file — seekable or not: a pipe, a
    response body. It is read once, front to back. ``size`` is exact; 0 is an
    empty object.

    Every upload URL is signed for its body's size and SHA-256, so a body is
    hashed before it is presigned and held in memory while it is sent: the
    whole object up to ``multipart_threshold`` bytes, in one presigned PUT;
    above it multipart, one part per request and ``part_concurrency`` parts
    in flight, whose failure aborts the session. The requests go through the
    data plane's ``Transfer``, which retries each through a freshly presigned
    URL; a retried PUT that finds its object stored (412) completes.

    ``on_session`` receives an open multipart session, to store where a
    restart finds it; with it set a failed upload is left open rather than
    aborted. ``resume`` continues that session, sending only the parts
    storage does not already hold."""
    if size < 0:
        raise ValueError("upload size must not be negative")
    reader = _reader(body)
    if size > multipart_threshold:
        return _upload_multipart(
            data,
            parent,
            key,
            content_type,
            size,
            reader,
            metadata,
            tags,
            part_concurrency,
            on_session,
            resume,
        )
    content = _read_exactly(reader, size)
    checksum = _sha256(content)
    allocated = data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            parent=parent,
            key=key,
            content_type=content_type,
            size_hint_bytes=size,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            checksum_value=checksum,
            metadata=metadata or {},
            tags=tags or {},
            transport=object_service_pb2.PRESIGN_TRANSPORT_PUT,
        )
    )
    if not allocated.upload_url.url:
        raise TransferError(_PUT, "", 0, "the server returned no upload URL")
    headers = {_HEADER_CONTENT_TYPE: content_type, _HEADER_CONTENT_LENGTH: str(size)}
    transfer = _transfer(data)
    name = allocated.object.name

    def regenerate() -> resource_pb2.PresignedUrl:
        return data.presign.regenerate_upload_url(
            presign_service_pb2.RegenerateUploadUrlRequest(name=name)
        ).upload_url

    def put(signed: resource_pb2.PresignedUrl) -> str:
        started = time.monotonic()
        with transfer.stream(_PUT, signed, headers, content) as resp:
            got = _etag(resp.headers)
        transfer.ended(_PUT, transfer.host_of(signed), size, started, None)
        return got

    try:
        etag = with_retries(transfer.attempts, allocated.upload_url, regenerate, put)
    except TransferError as err:
        # The URL refuses to overwrite: an earlier attempt stored the bytes
        # and only its answer was lost. CompleteObject reads the ETag itself.
        if not already_stored(err):
            raise
        etag = ""
    return data.object.complete_object(
        object_service_pb2.CompleteObjectRequest(
            name=allocated.object.name, etag=etag, checksum_value=checksum
        )
    )


def _session(
    init: multipart_service_pb2.InitiateMultipartUploadResponse, size: int
) -> UploadSession:
    part_size = init.recommended_part_size or DEFAULT_MULTIPART_THRESHOLD
    return UploadSession(
        object_name=init.object.name,
        upload_id=init.upload_id,
        part_size=part_size,
        total_parts=init.total_parts or -(-size // part_size),
    )


def _initiate_request(
    parent: str,
    key: str,
    content_type: str,
    size: int,
    metadata: dict[str, str] | None,
    tags: dict[str, str] | None,
) -> multipart_service_pb2.InitiateMultipartUploadRequest:
    return multipart_service_pb2.InitiateMultipartUploadRequest(
        parent=parent,
        key=key,
        content_type=content_type,
        size_bytes=size,
        checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
        metadata=metadata or {},
        tags=tags or {},
    )


def _stored_parts(data: DataPlane, session: UploadSession) -> dict[int, types_pb2.PartInfo]:
    """Every part storage holds for the session, all pages."""
    request = multipart_service_pb2.ListPartsRequest(
        object_name=session.object_name, upload_id=session.upload_id
    )
    return {p.part_number: p for p in pages(data.multipart_upload.list_parts, request, "parts")}


async def _astored_parts(
    data: AsyncDataPlane, session: UploadSession
) -> dict[int, types_pb2.PartInfo]:
    """``_stored_parts`` for the async clients."""
    request = multipart_service_pb2.ListPartsRequest(
        object_name=session.object_name, upload_id=session.upload_id
    )
    return {
        p.part_number: p async for p in apages(data.multipart_upload.list_parts, request, "parts")
    }


def _upload_multipart(
    data: DataPlane,
    parent: str,
    key: str,
    content_type: str,
    size: int,
    reader: IO[bytes],
    metadata: dict[str, str] | None,
    tags: dict[str, str] | None,
    concurrency: int,
    on_session: Callable[[UploadSession], None] | None,
    resume: UploadSession | None,
) -> types_pb2.Object:
    stored: dict[int, types_pb2.PartInfo] = {}
    if resume is not None:
        session = resume
        stored = _stored_parts(data, session)
    else:
        session = _session(
            data.multipart_upload.initiate_multipart_upload(
                _initiate_request(parent, key, content_type, size, metadata, tags)
            ),
            size,
        )
        if on_session is not None:
            on_session(session)
    resumable = on_session is not None or resume is not None
    name, upload_id = session.object_name, session.upload_id
    part_size, count = session.part_size, session.total_parts
    transfer = _transfer(data)

    def send(index: int, chunk: bytes) -> types_pb2.CompletedPart:
        checksum = _sha256(chunk)
        have = stored.get(index + 1)
        if have is not None and _holds(have, chunk):
            return types_pb2.CompletedPart(
                part_number=index + 1, etag=have.etag, checksum_value=checksum
            )

        def presign() -> resource_pb2.PresignedUrl:
            return data.multipart_upload.presign_part(
                multipart_service_pb2.PresignPartRequest(
                    object_name=name,
                    upload_id=upload_id,
                    part_number=index + 1,
                    checksum_value=checksum,
                )
            ).upload_url

        headers = {_HEADER_CONTENT_LENGTH: str(len(chunk))}

        def put(signed: resource_pb2.PresignedUrl) -> str:
            started = time.monotonic()
            with transfer.stream(_PUT, signed, headers, chunk) as resp:
                got = _etag(resp.headers)
            transfer.ended(_PUT, transfer.host_of(signed), len(chunk), started, None)
            return got

        etag = with_retries(transfer.attempts, None, presign, put)
        if not etag:
            raise TransferError(
                _PUT, "", 0, f"part {index + 1} returned no ETag; storage must expose it"
            )
        return types_pb2.CompletedPart(part_number=index + 1, etag=etag, checksum_value=checksum)

    workers = max(1, min(concurrency, count))
    try:
        # Parts are read here, in order, and handed to the pool: at most
        # ``workers`` in flight and one more being read.
        with ThreadPoolExecutor(max_workers=workers) as pool:
            futures: list[Future[types_pb2.CompletedPart]] = []
            pending: set[Future[types_pb2.CompletedPart]] = set()
            for index in range(count):
                if len(pending) >= workers:
                    done, pending = wait_futures(pending, return_when=FIRST_COMPLETED)
                    for finished in done:
                        finished.result()
                chunk = _read_exactly(reader, min(part_size, size - index * part_size))
                future = pool.submit(send, index, chunk)
                futures.append(future)
                pending.add(future)
            parts = [f.result() for f in futures]
        return data.multipart_upload.complete_multipart_upload(
            multipart_service_pb2.CompleteMultipartUploadRequest(
                object_name=name, upload_id=upload_id, parts=parts
            )
        )
    except BaseException:
        if resumable:
            raise
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


def download_stream(
    data: DataPlane, name: str, *, offset: int = 0, length: int = 0
) -> ObjectReader:
    """The object's content as a file-like ``ObjectReader``, streamed: the
    object is never held in memory whole. Close it, or use it in ``with``.

    ``offset`` and ``length`` read a byte range — ``length`` 0 to the end;
    ``RangeIgnoredError`` when storage answers with the whole object. A whole
    read is verified against the object's size and recorded checksum
    (``IntegrityError``)."""
    if offset < 0 or length < 0:
        raise ValueError("a range needs offset >= 0 and length >= 0")
    resp = data.object.download_object(
        # Bound to the object's ETag: a range, which the checksum cannot
        # verify, never splices in another object written at the key.
        object_service_pb2.DownloadObjectRequest(name=name, require_etag_match=True)
    )
    if not resp.download_url.url:
        raise TransferError(_GET, "", 0, "the server returned no download URL")
    ranged = offset != 0 or length != 0
    headers = {_HEADER_RANGE: range_header(offset, length)} if ranged else {}
    transfer = _transfer(data)
    started = time.monotonic()
    host = transfer.host_of(resp.download_url)

    def presign() -> resource_pb2.PresignedUrl:
        # Bound to the object's ETag as it is now: a replaced object is
        # reported, not read.
        again = data.object.download_object(
            object_service_pb2.DownloadObjectRequest(name=name, require_etag_match=True)
        )
        if again.object.etag != resp.object.etag:
            raise ObjectChangedError(f"{name} changed while its download was retried")
        return again.download_url

    def open_exchange(signed: resource_pb2.PresignedUrl) -> tuple[Any, Any]:
        exchange = transfer.stream(_GET, signed, headers)
        return exchange, exchange.__enter__()

    exchange, got = with_retries(transfer.attempts, resp.download_url, presign, open_exchange)
    try:
        if ranged:
            require_partial(got)
    except BaseException:
        exchange.__exit__(None, None, None)
        raise
    return ObjectReader(
        exchange,
        got,
        resp.object,
        verify=not ranged,
        ended=lambda moved, error: transfer.ended(_GET, host, moved, started, error),
    )


def download(data: DataPlane, name: str, *, offset: int = 0, length: int = 0) -> bytes:
    """``download_stream`` read whole: the content, or the range, as bytes."""
    with download_stream(data, name, offset=offset, length=length) as reader:
        return reader.read()


def lookup_object(data: DataPlane, uri: ObjectURI | str) -> types_pb2.Object:
    """The object a ``paladin://`` URI names, found by its key."""
    parsed = uri if isinstance(uri, ObjectURI) else ObjectURI.parse(uri)
    return data.object.lookup_object(
        object_service_pb2.LookupObjectRequest(parent=parsed.parent, key=parsed.key)
    )


def download_uri(
    data: DataPlane, uri: ObjectURI | str, *, offset: int = 0, length: int = 0
) -> ObjectReader:
    """``download_stream`` for the object a ``paladin://`` URI names."""
    return download_stream(data, lookup_object(data, uri).name, offset=offset, length=length)


# ─── asyncio ────────────────────────────────────────────────────────────────


async def _aread_exactly(reader: IO[bytes], length: int) -> bytes:
    """``_read_exactly`` off the event loop: a file read blocks."""
    return await asyncio.to_thread(_read_exactly, reader, length)


async def aupload(
    data: AsyncDataPlane,
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
    on_session: Callable[[UploadSession], None] | None = None,
    resume: UploadSession | None = None,
) -> types_pb2.Object:
    """``upload`` for the async clients (``connect_async``). The body is read
    in a worker thread, so a file read does not block the event loop."""
    if size < 0:
        raise ValueError("upload size must not be negative")
    reader = _reader(body)
    transfer = _transfer(data)
    if size > multipart_threshold:
        return await _aupload_multipart(
            data,
            transfer,
            parent,
            key,
            content_type,
            size,
            reader,
            metadata,
            tags,
            part_concurrency,
            on_session,
            resume,
        )
    content = await _aread_exactly(reader, size)
    checksum = _sha256(content)
    allocated = await data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            parent=parent,
            key=key,
            content_type=content_type,
            size_hint_bytes=size,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            checksum_value=checksum,
            metadata=metadata or {},
            tags=tags or {},
            transport=object_service_pb2.PRESIGN_TRANSPORT_PUT,
        )
    )
    if not allocated.upload_url.url:
        raise TransferError(_PUT, "", 0, "the server returned no upload URL")
    headers = {_HEADER_CONTENT_TYPE: content_type, _HEADER_CONTENT_LENGTH: str(size)}
    name = allocated.object.name

    async def regenerate() -> resource_pb2.PresignedUrl:
        regenerated = await data.presign.regenerate_upload_url(
            presign_service_pb2.RegenerateUploadUrlRequest(name=name)
        )
        return regenerated.upload_url

    async def put(signed: resource_pb2.PresignedUrl) -> str:
        started = time.monotonic()
        async with transfer.astream(_PUT, signed, headers, content) as resp:
            got = _etag(resp.headers)
        transfer.ended(_PUT, transfer.host_of(signed), size, started, None)
        return got

    try:
        etag = await awith_retries(transfer.attempts, allocated.upload_url, regenerate, put)
    except TransferError as err:
        # As in the sync form: the bytes landed and only the answer was lost.
        if not already_stored(err):
            raise
        etag = ""
    return await data.object.complete_object(
        object_service_pb2.CompleteObjectRequest(
            name=allocated.object.name, etag=etag, checksum_value=checksum
        )
    )


async def _aupload_multipart(
    data: AsyncDataPlane,
    transfer: Transfer,
    parent: str,
    key: str,
    content_type: str,
    size: int,
    reader: IO[bytes],
    metadata: dict[str, str] | None,
    tags: dict[str, str] | None,
    concurrency: int,
    on_session: Callable[[UploadSession], None] | None,
    resume: UploadSession | None,
) -> types_pb2.Object:
    stored: dict[int, types_pb2.PartInfo] = {}
    if resume is not None:
        session = resume
        stored = await _astored_parts(data, session)
    else:
        session = _session(
            await data.multipart_upload.initiate_multipart_upload(
                _initiate_request(parent, key, content_type, size, metadata, tags)
            ),
            size,
        )
        if on_session is not None:
            on_session(session)
    resumable = on_session is not None or resume is not None
    name, upload_id = session.object_name, session.upload_id
    part_size, count = session.part_size, session.total_parts

    async def send(index: int, chunk: bytes) -> types_pb2.CompletedPart:
        checksum = _sha256(chunk)
        have = stored.get(index + 1)
        if have is not None and _holds(have, chunk):
            return types_pb2.CompletedPart(
                part_number=index + 1, etag=have.etag, checksum_value=checksum
            )

        async def presign() -> resource_pb2.PresignedUrl:
            presigned = await data.multipart_upload.presign_part(
                multipart_service_pb2.PresignPartRequest(
                    object_name=name,
                    upload_id=upload_id,
                    part_number=index + 1,
                    checksum_value=checksum,
                )
            )
            return presigned.upload_url

        headers = {_HEADER_CONTENT_LENGTH: str(len(chunk))}

        async def put(signed: resource_pb2.PresignedUrl) -> str:
            started = time.monotonic()
            async with transfer.astream(_PUT, signed, headers, chunk) as resp:
                got = _etag(resp.headers)
            transfer.ended(_PUT, transfer.host_of(signed), len(chunk), started, None)
            return got

        etag = await awith_retries(transfer.attempts, None, presign, put)
        if not etag:
            raise TransferError(
                _PUT, "", 0, f"part {index + 1} returned no ETag; storage must expose it"
            )
        return types_pb2.CompletedPart(part_number=index + 1, etag=etag, checksum_value=checksum)

    workers = max(1, min(concurrency, count))
    tasks: list[asyncio.Task[types_pb2.CompletedPart]] = []
    try:
        pending: set[asyncio.Task[types_pb2.CompletedPart]] = set()
        for index in range(count):
            if len(pending) >= workers:
                done, pending = await asyncio.wait(pending, return_when=asyncio.FIRST_COMPLETED)
                for finished in done:
                    finished.result()
            chunk = await _aread_exactly(reader, min(part_size, size - index * part_size))
            task = asyncio.ensure_future(send(index, chunk))
            tasks.append(task)
            pending.add(task)
        parts = list(await asyncio.gather(*tasks))
        return await data.multipart_upload.complete_multipart_upload(
            multipart_service_pb2.CompleteMultipartUploadRequest(
                object_name=name, upload_id=upload_id, parts=parts
            )
        )
    except BaseException:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        if resumable:
            raise
        # Best effort, as in the sync form.
        try:
            await data.multipart_upload.abort_multipart_upload(
                multipart_service_pb2.AbortMultipartUploadRequest(
                    object_name=name, upload_id=upload_id
                )
            )
        except Exception:  # noqa: BLE001, S110 -- a failed abort must not mask the error
            pass
        raise


async def adownload_stream(
    data: AsyncDataPlane, name: str, *, offset: int = 0, length: int = 0
) -> AsyncObjectReader:
    """``download_stream`` for the async clients: an ``AsyncObjectReader``."""
    if offset < 0 or length < 0:
        raise ValueError("a range needs offset >= 0 and length >= 0")
    resp = await data.object.download_object(
        object_service_pb2.DownloadObjectRequest(name=name, require_etag_match=True)
    )
    if not resp.download_url.url:
        raise TransferError(_GET, "", 0, "the server returned no download URL")
    ranged = offset != 0 or length != 0
    headers = {_HEADER_RANGE: range_header(offset, length)} if ranged else {}
    transfer = _transfer(data)
    started = time.monotonic()
    host = transfer.host_of(resp.download_url)

    async def presign() -> resource_pb2.PresignedUrl:
        # As in the sync form: a replaced object is reported, not read.
        again = await data.object.download_object(
            object_service_pb2.DownloadObjectRequest(name=name, require_etag_match=True)
        )
        if again.object.etag != resp.object.etag:
            raise ObjectChangedError(f"{name} changed while its download was retried")
        return again.download_url

    async def open_exchange(signed: resource_pb2.PresignedUrl) -> tuple[Any, Any]:
        exchange = transfer.astream(_GET, signed, headers)
        return exchange, await exchange.__aenter__()

    exchange, got = await awith_retries(
        transfer.attempts, resp.download_url, presign, open_exchange
    )
    try:
        if ranged:
            require_partial(got)
    except BaseException:
        await exchange.__aexit__(None, None, None)
        raise
    return AsyncObjectReader(
        exchange,
        got,
        resp.object,
        verify=not ranged,
        ended=lambda moved, error: transfer.ended(_GET, host, moved, started, error),
    )


async def adownload(data: AsyncDataPlane, name: str, *, offset: int = 0, length: int = 0) -> bytes:
    """``adownload_stream`` read whole."""
    async with await adownload_stream(data, name, offset=offset, length=length) as reader:
        return await reader.read()


async def alookup_object(data: AsyncDataPlane, uri: ObjectURI | str) -> types_pb2.Object:
    """``lookup_object`` for the async clients."""
    parsed = uri if isinstance(uri, ObjectURI) else ObjectURI.parse(uri)
    return await data.object.lookup_object(
        object_service_pb2.LookupObjectRequest(parent=parsed.parent, key=parsed.key)
    )


async def adownload_uri(
    data: AsyncDataPlane, uri: ObjectURI | str, *, offset: int = 0, length: int = 0
) -> AsyncObjectReader:
    """``download_uri`` for the async clients."""
    obj = await alookup_object(data, uri)
    return await adownload_stream(data, obj.name, offset=offset, length=length)


# ─── Many objects ───────────────────────────────────────────────────────────

DEFAULT_BULK_CONCURRENCY = 8
"""Objects ``download_many`` and ``adownload_many`` fetch at once."""


def download_many(
    data: DataPlane, names: Iterable[str], *, concurrency: int = DEFAULT_BULK_CONCURRENCY
) -> Iterator[tuple[str, bytes | BaseException]]:
    """Download many objects, ``concurrency`` at a time, and yield each as
    ``(name, content)`` in the order they finish — or ``(name, error)`` for
    one that failed, so one bad object does not stop the rest. Each is held
    in memory whole; for large ones, use ``download_stream`` per object."""
    if concurrency < 1:
        raise ValueError("concurrency must be at least 1")
    names = iter(names)
    with ThreadPoolExecutor(max_workers=concurrency) as pool:
        pending: dict[Future[bytes], str] = {}
        for name in names:
            pending[pool.submit(download, data, name)] = name
            if len(pending) >= concurrency:
                done, _ = wait_futures(pending, return_when=FIRST_COMPLETED)
                for f in done:
                    yield _outcome(pending.pop(f), f)
        while pending:
            done, _ = wait_futures(pending, return_when=FIRST_COMPLETED)
            for f in done:
                yield _outcome(pending.pop(f), f)


def _outcome(name: str, f: Future[bytes]) -> tuple[str, bytes | BaseException]:
    err = f.exception()
    return (name, err) if err is not None else (name, f.result())


async def adownload_many(
    data: AsyncDataPlane, names: Iterable[str], *, concurrency: int = DEFAULT_BULK_CONCURRENCY
) -> AsyncIterator[tuple[str, bytes | BaseException]]:
    """``download_many`` for the async clients."""
    if concurrency < 1:
        raise ValueError("concurrency must be at least 1")
    pending: dict[asyncio.Task[bytes], str] = {}

    async def drain(until: int) -> AsyncIterator[tuple[str, bytes | BaseException]]:
        while len(pending) > until:
            done, _ = await asyncio.wait(pending, return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                name = pending.pop(task)
                err = task.exception()
                yield (name, err) if err is not None else (name, task.result())

    for name in names:
        pending[asyncio.ensure_future(adownload(data, name))] = name
        async for outcome in drain(concurrency - 1):
            yield outcome
    async for outcome in drain(0):
        yield outcome
