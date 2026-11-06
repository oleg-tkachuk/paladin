"""Transfer.astream takes any async iterable as a body, not only an iterator,
as its signature says."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator

import paladin
from paladin.data.v1 import object_service_pb2
from paladin.testing import FakePaladin

_KEY = "streamed"
_CHUNKS = (b"quarterly ", b"report")


class _OnlyIterable:
    """An async iterable that is not itself an iterator."""

    def __aiter__(self) -> AsyncIterator[bytes]:
        async def chunks() -> AsyncIterator[bytes]:
            for chunk in _CHUNKS:
                yield chunk

        return chunks()


def test_astream_sends_an_async_iterable_body() -> None:
    body = b"".join(_CHUNKS)
    with FakePaladin() as fake:
        data = fake.connect().data
        assert data is not None
        resp = data.object.upload_object(
            object_service_pb2.UploadObjectRequest(
                parent=str(fake.collection()),
                key=_KEY,
                content_type="text/plain",
                size_hint_bytes=len(body),
                checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, body),
            )
        )
        headers = dict(resp.upload_url.required_headers)
        headers["Content-Length"] = str(len(body))

        async def put() -> None:
            async with paladin.Transfer().astream("PUT", resp.upload_url, headers, _OnlyIterable()):
                pass

        asyncio.run(put())
        data.object.complete_object(object_service_pb2.CompleteObjectRequest(name=resp.object.name))
        assert fake.content(resp.object.name) == body
