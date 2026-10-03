"""Uploads bound to their size and checksum, downloads bound to the ETag —
exercised against paladin.testing, whose storage enforces both."""

from __future__ import annotations

import io
from collections.abc import Iterator

import pytest

import paladin
from paladin import CHECKSUM_MD5, CHECKSUM_SHA256, download, upload
from paladin.testing import PART_SIZE, FakePaladin
from paladin.transfer import _with_required


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


class _OneWayStream(io.RawIOBase):
    """A body that can be read once and never sought: a pipe."""

    def __init__(self, data: bytes) -> None:
        self._data = io.BytesIO(data)

    def readable(self) -> bool:
        return True

    def readinto(self, b: bytearray) -> int:  # type: ignore[override]
        chunk = self._data.read(len(b))
        b[: len(chunk)] = chunk
        return len(chunk)


@pytest.mark.parametrize(
    ("key", "body", "stream"),
    [
        ("empty", b"", False),
        ("small", b"hello", False),
        ("a pipe", b"piped body", True),
        ("multipart", b"z" * (2 * PART_SIZE + 7), False),
        ("a multipart pipe", b"q" * (PART_SIZE + 3), True),
    ],
)
def test_upload_binds_every_body(fake: FakePaladin, key: str, body: bytes, stream: bool) -> None:
    """Every upload URL is signed for its body's size and SHA-256 and the
    fake's storage refuses anything else, so a successful upload is one that
    sent exactly what it declared. An empty object used to be refused."""
    p = fake.connect()
    source = _OneWayStream(body) if stream else body
    obj = upload(
        p.data,
        parent=str(fake.collection()),
        key=key,
        content_type="application/octet-stream",
        body=source,  # type: ignore[arg-type]
        size=len(body),
        multipart_threshold=PART_SIZE,
    )
    assert obj.size_bytes == len(body)
    assert fake.content(obj.name) == body


def test_download_is_bound_to_the_etag(fake: FakePaladin) -> None:
    """download asks for an ETag-bound URL and sends the If-Match it needs;
    the fake's storage refuses a GET without the right one."""
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"etag-bound")
    assert download(p.data, obj.name, offset=5) == b"bound"


def test_signed_headers_replace_the_callers_whatever_their_case() -> None:
    """Header names are case-insensitive: a caller's content-type is replaced
    by the signed Content-Type, not sent beside it as a second value."""
    sent = _with_required(
        {"content-type": "text/plain", "X-Extra": "1", "host": "wrong"},
        "s3.example.com",
        {"Content-Type": "image/png", "If-None-Match": "*"},
    )
    assert sent == {
        "X-Extra": "1",
        "Host": "s3.example.com",
        "Content-Type": "image/png",
        "If-None-Match": "*",
    }


def test_checksum() -> None:
    assert (
        paladin.checksum(CHECKSUM_SHA256, b"hello")
        == "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="
    )
    assert paladin.checksum(CHECKSUM_MD5, b"hello") == "XUFAKrxLKna5cZ2REBfFkg=="
    with pytest.raises(ValueError):
        paladin.checksum("SHA1", b"")
