"""The control half of a multipart upload whose bytes a browser sends."""

from __future__ import annotations

import asyncio
import base64
import hashlib
import urllib.request
from types import SimpleNamespace

import pytest

from paladin import (
    Endpoints,
    NoPartSplitError,
    abegin_multipart,
    abort_multipart,
    acomplete_multipart,
    apresign_part,
    begin_multipart,
    complete_multipart,
    connect_async,
    download,
    presign_part,
)
from paladin.data.v1 import multipart_service_pb2, types_pb2
from paladin.testing import PART_SIZE, FakePaladin

BODY = b"b" * (2 * PART_SIZE + 3)


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def _put(url, headers, chunk: bytes) -> str:  # type: ignore[no-untyped-def]
    """What the browser does with a part URL: PUT the bytes, read the ETag."""
    request = urllib.request.Request(url, data=chunk, method="PUT", headers=dict(headers))
    with urllib.request.urlopen(request) as resp:
        return resp.headers["ETag"]


def _chunks(part_size: int, total: int):  # type: ignore[no-untyped-def]
    for i in range(total):
        chunk = BODY[i * part_size : (i + 1) * part_size]
        yield i + 1, chunk, base64.b64encode(hashlib.sha256(chunk).digest()).decode()


# A browser sends the parts while this process holds the credentials. The
# ETags arrive quoted, as a header carries them, and a part is presigned twice,
# as one whose URL expired would be.
def test_a_browser_upload_through_the_control_half(fake: FakePaladin) -> None:
    data = fake.connect().data
    session = begin_multipart(
        data,
        parent=str(fake.collection()),
        key="upload.bin",
        content_type="application/octet-stream",
        size=len(BODY),
    )
    assert (session.part_size, session.total_parts) == (PART_SIZE, 3)
    parts = []
    for number, chunk, checksum in _chunks(session.part_size, session.total_parts):
        presign_part(data, session, number, checksum)
        signed = presign_part(data, session, number, checksum)
        etag = _put(signed.url, signed.required_headers, chunk)
        parts.append(
            types_pb2.CompletedPart(
                part_number=number, etag=f'"{etag.strip(chr(34))}"', checksum_value=checksum
            )
        )
    obj = complete_multipart(data, session, parts)
    assert (obj.name, obj.size_bytes) == (session.object_name, len(BODY))
    assert download(data, obj.name) == BODY


def test_the_async_control_half(fake: FakePaladin) -> None:
    async def run() -> str:
        data = connect_async(Endpoints(data=fake.url)).data
        session = await abegin_multipart(
            data,
            parent=str(fake.collection()),
            key="async.bin",
            content_type="application/octet-stream",
            size=len(BODY),
        )
        parts = []
        for number, chunk, checksum in _chunks(session.part_size, session.total_parts):
            signed = await apresign_part(data, session, number, checksum)
            etag = await asyncio.to_thread(_put, signed.url, signed.required_headers, chunk)
            parts.append(
                types_pb2.CompletedPart(part_number=number, etag=etag, checksum_value=checksum)
            )
        return (await acomplete_multipart(data, session, parts)).name

    name = asyncio.run(run())
    assert download(fake.connect().data, name) == BODY


def test_abort_is_safe_to_repeat(fake: FakePaladin) -> None:
    data = fake.connect().data
    session = begin_multipart(
        data,
        parent=str(fake.collection()),
        key="dropped.bin",
        content_type="application/octet-stream",
        size=2 * PART_SIZE,
    )
    abort_multipart(data, session)
    abort_multipart(data, session)


# Every part URL is signed for the size the server recommended: an answer
# naming none is refused, and the upload it opened is aborted.
def test_begin_refuses_an_answer_with_no_split() -> None:
    aborted: list[str] = []
    multipart = SimpleNamespace(
        initiate_multipart_upload=lambda request: (
            multipart_service_pb2.InitiateMultipartUploadResponse(
                object=types_pb2.Object(name="tenants/t/collections/c/objects/o"), upload_id="u1"
            )
        ),
        abort_multipart_upload=lambda request: aborted.append(request.upload_id),
    )
    with pytest.raises(NoPartSplitError):
        begin_multipart(
            SimpleNamespace(multipart_upload=multipart),  # type: ignore[arg-type]
            parent="tenants/t/collections/c",
            content_type="text/plain",
            size=10,
        )
    assert aborted == ["u1"]
