"""Transfer: split horizon, redirects, errors; streamed uploads; ranged and
verified downloads — against the fake data plane and storage."""

from __future__ import annotations

import base64
import hashlib
import io
from collections.abc import Iterator
from datetime import datetime, timezone
from urllib.parse import urlsplit

import pytest
from data_plane_fake import PARENT, Fake

import paladin
import paladin.transfer as transfer_module
from paladin import (
    CHECKSUM_CRC32C,
    CHECKSUM_MD5,
    CHECKSUM_SHA256,
    Endpoints,
    IntegrityError,
    RangeIgnoredError,
    Transfer,
    TransferError,
    connect,
    download,
    download_stream,
    upload,
)
from paladin.common.v1 import resource_pb2
from paladin.data.v1 import types_pb2
from paladin.transfer import ERROR_BODY_LIMIT

# A public name nothing resolves: the transfer must reach the fake storage at
# its real address and still name this host.
SIGNED_ORIGIN = "http://storage.public.example:9000"
BODY = b"verified content"


def _data(fake: Fake, transfer: Transfer | None = None):  # type: ignore[no-untyped-def]
    return connect(Endpoints(data=fake.base), transfer=transfer).data


def _upload(data, body: bytes = BODY, key: str = "k", **kw) -> types_pb2.Object:  # type: ignore[no-untyped-def]
    return upload(
        data, parent=PARENT, key=key, content_type="text/plain", body=body, size=len(body), **kw
    )


def _b64(b: bytes) -> str:
    return base64.b64encode(b).decode()


class _Pipe(io.RawIOBase):
    """A stream that cannot seek, as a pipe or a response body."""

    def __init__(self, data: bytes) -> None:
        self._src = io.BytesIO(data)

    def readable(self) -> bool:
        return True

    def readinto(self, b) -> int:  # type: ignore[no-untyped-def]
        chunk = self._src.read(min(len(b), 3))  # short reads, as a pipe gives
        b[: len(chunk)] = chunk
        return len(chunk)


def test_split_horizon_sends_to_the_internal_address_with_the_signed_host(fake: Fake) -> None:
    fake.signed_origin = SIGNED_ORIGIN
    data = _data(fake, Transfer(split_horizon=(SIGNED_ORIGIN, fake.base)))
    obj = _upload(data)
    assert download(data, obj.name) == BODY
    assert fake.hosts == [urlsplit(SIGNED_ORIGIN).netloc] * 2


def test_rewrite_keeps_the_signed_host(fake: Fake) -> None:
    fake.signed_origin = SIGNED_ORIGIN
    internal = urlsplit(fake.base).netloc

    def rewrite(url: str) -> str:
        return urlsplit(url)._replace(netloc=internal).geturl()

    _upload(_data(fake, Transfer(rewrite=rewrite)))
    assert fake.hosts == [urlsplit(SIGNED_ORIGIN).netloc]


def test_split_horizon_leaves_other_origins_alone(fake: Fake) -> None:
    _upload(_data(fake, Transfer(split_horizon=("http://elsewhere.example", "http://127.0.0.1:1"))))
    assert len(fake.hosts) == 1


@pytest.mark.parametrize(
    "origin", ["", "storage:9000", "ftp://storage", "http://storage/path", "http://storage?x=1"]
)
def test_split_horizon_refuses_an_invalid_origin(origin: str) -> None:
    with pytest.raises(ValueError):
        Transfer(split_horizon=(origin, "http://ok"))


def test_split_horizon_and_rewrite_exclude_each_other() -> None:
    with pytest.raises(ValueError):
        Transfer(split_horizon=("http://a", "http://b"), rewrite=lambda u: u)


def test_a_redirect_is_refused_not_followed(fake: Fake) -> None:
    data = _data(fake)
    obj = _upload(data)
    fake.redirect_to = fake.base + "/storage/elsewhere"
    with pytest.raises(TransferError) as err:
        download(data, obj.name)
    assert err.value.status == 307
    assert len(fake.hosts) == 2, "the redirect was followed"


def test_transfer_error_quotes_a_capped_body_and_the_host(fake: Fake) -> None:
    data = _data(fake)
    fake.refuse_with = (403, b"x" * ERROR_BODY_LIMIT * 2)
    with pytest.raises(TransferError) as err:
        download(data, f"{PARENT}/objects/k")
    e = err.value
    assert (e.method, e.host, e.status, len(e.body)) == (
        "GET",
        urlsplit(fake.base).netloc,
        403,
        ERROR_BODY_LIMIT,
    )


@pytest.mark.parametrize(
    ("body", "threshold", "parts"),
    [
        (b"a stream of bytes", 1 << 20, None),
        (b"0123456789", 8, [b"0123", b"4567", b"89"]),
    ],
)
def test_upload_from_a_stream(fake: Fake, body: bytes, threshold: int, parts) -> None:  # type: ignore[no-untyped-def]
    fake.part_size = 4
    obj = upload(
        _data(fake),
        parent=PARENT,
        key="s",
        content_type="text/plain",
        body=_Pipe(body),
        size=len(body),
        multipart_threshold=threshold,
    )
    if parts is None:
        assert fake.blobs["/s?"] == body
        assert fake.checksums[obj.name] == _b64(hashlib.sha256(body).digest())
    else:
        assert [fake.blobs[f"/mp?part={i + 1}"] for i in range(len(parts))] == parts


def test_upload_fails_on_a_short_stream(fake: Fake) -> None:
    fake.part_size = 4
    with pytest.raises(ValueError, match="short"):
        upload(
            _data(fake),
            parent=PARENT,
            key="s",
            content_type="text/plain",
            body=_Pipe(b"012345"),
            size=10,
            multipart_threshold=8,
        )
    assert fake.aborted == 1


@pytest.mark.parametrize(
    ("offset", "length", "want"),
    [(2, 3, b"234"), (7, 0, b"789"), (0, 2, b"01")],
)
def test_download_reads_a_range(fake: Fake, offset: int, length: int, want: bytes) -> None:
    data = _data(fake)
    obj = _upload(data, b"0123456789")
    assert download(data, obj.name, offset=offset, length=length) == want


def test_download_refuses_an_ignored_range(fake: Fake) -> None:
    data = _data(fake)
    obj = _upload(data, b"0123456789")
    fake.ignore_range = True
    with pytest.raises(RangeIgnoredError):
        download(data, obj.name, offset=2, length=3)


def test_download_refuses_a_negative_range(fake: Fake) -> None:
    with pytest.raises(ValueError):
        download(_data(fake), f"{PARENT}/objects/k", offset=-1)


def test_download_without_a_url(fake: Fake) -> None:
    fake.no_url = True
    with pytest.raises(TransferError, match="no download URL"):
        download(_data(fake), f"{PARENT}/objects/k")


_WRONG = _b64(hashlib.sha256(b"something else").digest())


@pytest.mark.parametrize(
    ("size", "algorithm", "value", "offset", "mismatch"),
    [
        (len(BODY), CHECKSUM_SHA256, _b64(hashlib.sha256(BODY).digest()), 0, None),
        (len(BODY), CHECKSUM_MD5, _b64(hashlib.md5(BODY, usedforsecurity=False).digest()), 0, None),
        (len(BODY), "", "", 0, None),
        (len(BODY), CHECKSUM_SHA256, "abc-3", 0, None),
        (len(BODY), CHECKSUM_SHA256, "AAAA", 0, None),
        (len(BODY), "XXH3", "AAAA", 0, None),
        (len(BODY), CHECKSUM_SHA256, _WRONG, 0, CHECKSUM_SHA256),
        (len(BODY) + 1, CHECKSUM_SHA256, _b64(hashlib.sha256(BODY).digest()), 0, "size"),
        (len(BODY), CHECKSUM_SHA256, _WRONG, 1, None),
    ],
    ids=[
        "sha256",
        "md5",
        "none recorded",
        "multipart composite skipped",
        "wrong length skipped",
        "unknown algorithm skipped",
        "wrong checksum",
        "wrong size",
        "a range is not verified",
    ],
)
def test_download_verifies_the_whole_object(  # type: ignore[no-untyped-def]
    fake: Fake, size, algorithm, value, offset, mismatch
) -> None:
    data = _data(fake)
    obj = _upload(data)
    fake.described = types_pb2.Object(name=obj.name, size_bytes=size, content_type="text/plain")
    if algorithm:
        fake.described.checksum.algorithm = algorithm
        fake.described.checksum.value = value
    if mismatch:
        with pytest.raises(IntegrityError) as err:
            download(data, obj.name, offset=offset)
        assert err.value.what == mismatch
    else:
        got = download(data, obj.name, offset=offset)
        assert got == BODY[offset:]


def test_download_stream_is_a_file_with_type_and_length(fake: Fake) -> None:
    data = _data(fake)
    obj = _upload(data, b"0123456789")
    with download_stream(data, obj.name, offset=4) as r:
        assert (r.content_length, r.content_type) == (6, "application/octet-stream")
        assert r.read(2) == b"45"
        assert b"".join(r.chunks()) == b"6789"
    assert r.closed


def _chunks(reader) -> Iterator[bytes]:  # type: ignore[no-untyped-def]
    yield from reader.chunks()


def test_download_stream_verifies_through_chunks(fake: Fake) -> None:
    data = _data(fake)
    obj = _upload(data)
    fake.described = types_pb2.Object(name=obj.name, size_bytes=len(BODY))
    fake.described.checksum.algorithm = CHECKSUM_SHA256
    fake.described.checksum.value = _WRONG
    with download_stream(data, obj.name) as r, pytest.raises(IntegrityError):
        list(_chunks(r))


def test_the_given_transfer_is_used(fake: Fake) -> None:
    sent: list[str] = []

    class Counting(Transfer):
        def stream(self, method, signed, headers=None, content=None):  # type: ignore[no-untyped-def,override]
            sent.append(method)
            return super().stream(method, signed, headers, content)

    data = _data(fake, Counting())
    download(data, _upload(data).name)
    assert sent == ["PUT", "GET"]


def _crc32c_of(body: bytes) -> str:
    import google_crc32c

    return _b64(google_crc32c.Checksum(body).digest())


@pytest.mark.parametrize(("value", "ok"), [(None, True), ("wrong", False)])
def test_download_verifies_crc32c(fake: Fake, value: str | None, ok: bool) -> None:
    # Both cases need the extra: without it CRC32C is not verified at all,
    # which test_crc32c_is_not_verified_without_the_extra pins.
    pytest.importorskip("google_crc32c")
    data = _data(fake)
    obj = _upload(data)
    fake.described = types_pb2.Object(name=obj.name, size_bytes=len(BODY))
    fake.described.checksum.algorithm = CHECKSUM_CRC32C
    # A wrong four-byte digest: decodes to the right length, so it is checked.
    fake.described.checksum.value = _crc32c_of(BODY) if value is None else _b64(b"\x00\x01\x02\x03")
    if ok:
        assert download(data, obj.name) == BODY
    else:
        with pytest.raises(IntegrityError):
            download(data, obj.name)


def test_crc32c_is_not_verified_without_the_extra(
    fake: Fake, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setitem(transfer_module._DIGESTS, CHECKSUM_CRC32C, lambda: None)
    data = _data(fake)
    obj = _upload(data)
    fake.described = types_pb2.Object(name=obj.name, size_bytes=len(BODY))
    fake.described.checksum.algorithm = CHECKSUM_CRC32C
    fake.described.checksum.value = _b64(b"\x00\x01\x02\x03")
    assert download(data, obj.name) == BODY


# Consumers parsed expires_at_rfc3339 themselves; presign_expiry is the reading
# the SDK's own retry logic uses.
@pytest.mark.parametrize(
    ("raw", "want"),
    [
        ("2026-10-06T12:00:00Z", datetime(2026, 10, 6, 12, tzinfo=timezone.utc)),
        ("2026-10-06T15:00:00+03:00", datetime(2026, 10, 6, 12, tzinfo=timezone.utc)),
        ("", None),
        ("tomorrow", None),
    ],
    ids=["an RFC 3339 instant", "an offset", "none sent", "not a time"],
)
def test_presign_expiry(raw: str, want: datetime | None) -> None:
    assert paladin.presign_expiry(resource_pb2.PresignedUrl(expires_at_rfc3339=raw)) == want
