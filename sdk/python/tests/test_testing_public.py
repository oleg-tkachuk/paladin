"""Public collections in the fake, as the server and its store keep them: the
fake names every object, serves it unsigned at its public_url with the
collection's Cache-Control, and refuses what the server refuses."""

from __future__ import annotations

import re
import urllib.error
import urllib.request
from collections.abc import Iterator

import pytest

import paladin
from paladin import FailedPreconditionError
from paladin.common.v1 import error_reason_pb2, resource_pb2
from paladin.data.v1 import object_service_pb2
from paladin.testing import PUBLIC_CACHE_CONTROL, FakePaladin

PUBLIC_KEY = re.compile(r"^[a-z2-7]{26}$")
"""A key the server names a public object with."""
CONTENT_TYPE = "image/jpeg"
BODY = b"a published picture"
NOT_FOUND = 404
OK = 200


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


def _get(url: str) -> tuple[int, bytes, str]:
    try:
        with urllib.request.urlopen(url) as resp:
            return resp.status, resp.read(), resp.headers.get("Cache-Control", "")
    except urllib.error.HTTPError as e:
        return e.code, b"", ""


def test_a_public_object_is_served_at_its_url(fake: FakePaladin) -> None:
    data = fake.connect().data
    obj = paladin.upload(
        data,
        parent=str(fake.public_collection()),
        key="",
        content_type=CONTENT_TYPE,
        body=BODY,
        size=len(BODY),
    )
    assert PUBLIC_KEY.match(obj.key) and obj.public_url
    assert _get(obj.public_url) == (OK, BODY, PUBLIC_CACHE_CONTROL)
    data.object.delete_object(  # type: ignore[union-attr]
        object_service_pb2.DeleteObjectRequest(
            name=obj.name, resource_version=obj.resource_version, permanent=True
        )
    )
    assert _get(obj.public_url)[0] == NOT_FOUND


def test_a_client_key_is_refused(fake: FakePaladin) -> None:
    with pytest.raises(FailedPreconditionError) as caught:
        fake.connect().data.object.upload_object(  # type: ignore[union-attr]
            object_service_pb2.UploadObjectRequest(
                parent=str(fake.public_collection()),
                key="chosen.jpg",
                content_type=CONTENT_TYPE,
                size_hint_bytes=1,
                checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
                checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, b"x"),
            )
        )
    assert caught.value.reason == error_reason_pb2.ERROR_REASON_PUBLIC_COLLECTION_RULE


def test_a_delete_to_the_trash_is_refused(fake: FakePaladin) -> None:
    obj = fake.put(fake.public_collection(), "", CONTENT_TYPE, BODY)
    with pytest.raises(FailedPreconditionError) as caught:
        fake.connect().data.object.delete_object(  # type: ignore[union-attr]
            object_service_pb2.DeleteObjectRequest(
                name=obj.name, resource_version=obj.resource_version
            )
        )
    assert caught.value.reason == error_reason_pb2.ERROR_REASON_PUBLIC_COLLECTION_RULE
    assert _get(obj.public_url)[0] == OK, "a refused delete stopped the object being served"


def test_the_upload_url_is_bound_to_the_cache_control(fake: FakePaladin) -> None:
    resp = fake.connect().data.object.upload_object(  # type: ignore[union-attr]
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.public_collection()),
            content_type=CONTENT_TYPE,
            size_hint_bytes=1,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, b"x"),
        )
    )
    assert resp.upload_url.required_headers["Cache-Control"] == PUBLIC_CACHE_CONTROL


def test_a_private_object_has_no_public_url(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "mine", CONTENT_TYPE, BODY)
    assert obj.key == "mine" and not obj.public_url


def test_a_public_object_is_served_as_soon_as_its_bytes_land(fake: FakePaladin) -> None:
    """The store serves what a PUT stored before CompleteObject: it knows
    nothing of Paladin's states."""
    resp = fake.connect().data.object.upload_object(  # type: ignore[union-attr]
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.public_collection()),
            content_type=CONTENT_TYPE,
            size_hint_bytes=1,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, b"x"),
        )
    )
    assert _get(resp.object.public_url)[0] == NOT_FOUND
    req = urllib.request.Request(resp.upload_url.url, data=b"x", method="PUT")
    for name, value in resp.upload_url.required_headers.items():
        req.add_header(name, value)
    with urllib.request.urlopen(req):
        pass
    assert _get(resp.object.public_url)[:2] == (OK, b"x")
