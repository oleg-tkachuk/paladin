"""An idempotency key set for a block must not leak onto calls that differ.

The block's key went on every call made inside it, and the server answered a
repeated key with its first response: ``download_many`` under a key read the
first object's bytes under every name, and an async multipart upload presigned
every part with the first part's URL. The fake now answers keys as the server
does, so these fail if the key leaks again."""

from __future__ import annotations

import asyncio

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin import (
    Endpoints,
    UploadItem,
    adownload_many,
    aupload,
    connect_async,
    download,
    download_many,
    idempotency_key,
    upload_many,
)
from paladin.data.v1 import object_service_pb2
from paladin.testing import PART_SIZE, FakePaladin

OPERATION_KEY = "nightly-export-2026-10-06"


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def _objects(fake: FakePaladin, n: int) -> dict[str, bytes]:
    return {
        fake.put(
            fake.collection(), f"k{i}", "text/plain", f"body {i}".encode()
        ).name: f"body {i}".encode()
        for i in range(n)
    }


def test_download_many_under_a_key_reads_each_object(fake: FakePaladin) -> None:
    want = _objects(fake, 4)
    with idempotency_key(OPERATION_KEY):
        got = dict(download_many(fake.connect().data, list(want), concurrency=2))
    assert got == want


def test_adownload_many_under_a_key_reads_each_object(fake: FakePaladin) -> None:
    want = _objects(fake, 4)

    async def run() -> dict[str, object]:
        data = connect_async(Endpoints(data=fake.url)).data
        with idempotency_key(OPERATION_KEY):
            return {n: b async for n, b in adownload_many(data, list(want), concurrency=2)}

    assert asyncio.run(run()) == want


def test_upload_many_under_a_key_creates_each_object(fake: FakePaladin) -> None:
    items = [
        UploadItem(
            parent=str(fake.collection()),
            content_type="text/plain",
            body=f"u{i}".encode(),
            size=2,
            key=f"u{i}",
        )
        for i in range(3)
    ]
    with idempotency_key(OPERATION_KEY):
        got = list(upload_many(fake.connect().data, items, concurrency=2))
    names = [obj.name for _, obj in got]  # type: ignore[union-attr]
    assert len(set(names)) == len(items), names


def test_async_multipart_under_a_key_presigns_each_part(fake: FakePaladin) -> None:
    body = b"p" * (2 * PART_SIZE + 3)

    async def run() -> str:
        data = connect_async(Endpoints(data=fake.url)).data
        with idempotency_key(OPERATION_KEY):
            obj = await aupload(
                data,
                parent=str(fake.collection()),
                key="big.bin",
                content_type="application/octet-stream",
                body=body,
                size=len(body),
                multipart_threshold=PART_SIZE,
            )
        return obj.name

    name = asyncio.run(run())
    assert download(fake.connect().data, name) == body


def test_the_fake_refuses_a_key_reused_for_another_request(fake: FakePaladin) -> None:
    a, b = list(_objects(fake, 2))
    data = fake.connect().data
    with idempotency_key(OPERATION_KEY):
        data.object.download_object(object_service_pb2.DownloadObjectRequest(name=a))
        data.object.download_object(object_service_pb2.DownloadObjectRequest(name=a))
        with pytest.raises(ConnectError) as exc:
            data.object.download_object(object_service_pb2.DownloadObjectRequest(name=b))
    assert exc.value.code == Code.INVALID_ARGUMENT
