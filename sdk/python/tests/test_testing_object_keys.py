"""The fake holds an object key to the server's rule, against
sdk/testdata/object_keys.json, which the server's tests read too."""

from __future__ import annotations

import json
from collections.abc import Iterator
from pathlib import Path
from typing import cast

import pytest

from paladin import InvalidArgumentError, testing
from paladin.common.v1 import resource_pb2
from paladin.data.v1 import multipart_service_pb2, object_service_pb2
from paladin.testing import FakePaladin

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "object_keys.json"
CONTENT_TYPE = "application/octet-stream"
EMPTY_SHA256 = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
"""The SHA-256 of no bytes, the checksum an upload binds its URL to."""
MULTIPART_PREFIX = "multipart/"
"""Puts a multipart upload's key beside, not on, the plain upload's."""

_KEYS = json.loads(TABLE.read_text())


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


def _upload(fake: FakePaladin, key: str) -> None:
    fake.connect().data.object.upload_object(  # type: ignore[union-attr]
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.collection()),
            key=key,
            content_type=CONTENT_TYPE,
            checksum_value=EMPTY_SHA256,
        )
    )


def _initiate(fake: FakePaladin, key: str) -> None:
    fake.connect().data.multipart_upload.initiate_multipart_upload(  # type: ignore[union-attr]
        multipart_service_pb2.InitiateMultipartUploadRequest(
            parent=str(fake.collection()),
            key=key,
            content_type=CONTENT_TYPE,
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            size_bytes=1,
        )
    )


@pytest.mark.parametrize("key", _KEYS["canonical"])
def test_a_canonical_key_is_stored(fake: FakePaladin, key: str) -> None:
    _upload(fake, key)
    _initiate(fake, MULTIPART_PREFIX + key)


@pytest.mark.parametrize("key", _KEYS["refused"])
def test_a_key_the_server_refuses_is_refused(fake: FakePaladin, key: str) -> None:
    with pytest.raises(InvalidArgumentError):
        _upload(fake, key)
    with pytest.raises(InvalidArgumentError):
        _initiate(fake, key)


def _length_key(case: dict[str, object]) -> str:
    """The key of a lengths case: its segments joined by "/", each its unit
    repeated to the segment's bytes."""
    unit = str(case["unit"])
    unit_bytes = len(unit.encode())
    segments: list[str] = []
    for size in cast(list[int], case["segments"]):
        assert size % unit_bytes == 0, f"{unit!r}: {size} bytes is not a whole number of units"
        segments.append(unit * (size // unit_bytes))
    return "/".join(segments)


@pytest.mark.parametrize("case", _KEYS["lengths"], ids=lambda c: f"{c['unit']}{c['segments']}")
def test_a_key_is_bounded_in_utf8_bytes(case: dict[str, object]) -> None:
    # A key at the limit leaves no room for a prefix to keep the multipart
    # upload off the plain one's key, so each goes to a fake of its own.
    key = _length_key(case)
    for call in (_upload, _initiate):
        with FakePaladin() as f:
            if case["canonical"]:
                call(f, key)
            else:
                with pytest.raises(InvalidArgumentError):
                    call(f, key)


def test_the_fakes_limits_are_the_tables() -> None:
    assert _KEYS["max_key_bytes"] == testing._MAX_KEY_BYTES
    assert _KEYS["max_segment_bytes"] == testing._MAX_SEGMENT_BYTES


def test_a_lone_surrogate_is_refused() -> None:
    # No UTF-8 encodes one, so it never reaches the server; JSON leaves it to
    # each consumer.
    assert testing._key_refusal("a\ud800") != ""
