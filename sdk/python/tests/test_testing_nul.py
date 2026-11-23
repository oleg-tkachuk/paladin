"""The fake refuses request text holding NUL (U+0000) as the server does:
INVALID_ARGUMENT naming the field, before any handler and before validation.
It ran protovalidate alone, so a test passed on a call the server refuses."""

from __future__ import annotations

from collections.abc import Iterator

import pytest

from paladin import InvalidArgumentError
from paladin.data.v1 import object_service_pb2
from paladin.testing import FakePaladin

CONTENT_TYPE = "application/octet-stream"
ONE_BYTE_SHA256 = "LXEWQrcmsEQBYnyp+6wy9chTD7GQPMTbAiWHF5IaSIE="
"""The base64 SHA-256 of a one-byte upload."""
WITH_NUL = "a\x00b"
"""Request text holding NUL between printable characters."""


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


def _upload(fake: FakePaladin, **fields: object) -> object_service_pb2.UploadObjectRequest:
    """A valid upload request, with ``fields`` set over it."""
    request = object_service_pb2.UploadObjectRequest(
        parent=str(fake.collection()),
        key="k",
        content_type=CONTENT_TYPE,
        size_hint_bytes=1,
        checksum_value=ONE_BYTE_SHA256,
    )
    for name, value in fields.items():
        if isinstance(value, dict):
            getattr(request, name).update(value)
        else:
            setattr(request, name, value)
    return request


@pytest.mark.parametrize(
    ("fields", "want"),
    [
        ({"key": WITH_NUL}, "key: holds a NUL character"),
        ({"metadata": {"m": WITH_NUL}}, "metadata.value: holds a NUL character"),
        # Ahead of validation, as on the server: the missing content type is
        # not what the caller is told.
        ({"key": WITH_NUL, "content_type": ""}, "key: holds a NUL character"),
    ],
    ids=["a key", "a metadata value", "a request validation also refuses"],
)
def test_the_fake_refuses_request_text_holding_nul(
    fake: FakePaladin, fields: dict[str, object], want: str
) -> None:
    data = fake.connect().data
    with pytest.raises(InvalidArgumentError) as caught:
        data.object.upload_object(_upload(fake, **fields))  # type: ignore[union-attr]
    assert caught.value.message == want
    listed = data.object.list_objects(  # type: ignore[union-attr]
        object_service_pb2.ListObjectsRequest(parent=str(fake.collection()))
    )
    assert not listed.objects, "the fake created an object for a refused request"
