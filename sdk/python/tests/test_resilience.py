"""Retried presigned transfers and resumed multipart uploads, against the fake's
storage answering with injected faults."""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable, Iterator

import pytest

import paladin
from paladin import (
    DEFAULT_TRANSFER_ATTEMPTS,
    ObjectChangedError,
    Transfer,
    TransferError,
    UploadSession,
    _retry,
    adownload,
    already_stored,
    aupload,
    download,
    expired,
    upload,
    workflows,
)
from paladin.common.v1 import resource_pb2
from paladin.data.v1 import types_pb2
from paladin.testing import EXPIRED_BODY, PART_SIZE, FakePaladin, StorageOp

PUT = "PUT"
GET = "GET"
FORBIDDEN = 403
BAD_REQUEST = 400
PRECONDITION_FAILED = 412
UNAVAILABLE = 503
REGENERATE = "/paladin.data.v1.PresignService/RegenerateUploadUrl"
PRESIGN_PART = "/paladin.data.v1.MultipartUploadService/PresignPart"
ABORT = "/paladin.data.v1.MultipartUploadService/AbortMultipartUpload"
DOWNLOAD = "/paladin.data.v1.ObjectService/DownloadObject"
CONTENT_TYPE = "application/octet-stream"
SMALL = b"small body"
# Three parts: two whole, one of three bytes.
MULTIPART_SIZE = 2 * PART_SIZE + 3
SECOND_PART = "part=2"
# The real wait, kept before the fixture below replaces it.
BACKOFF = _retry.backoff


@pytest.fixture(autouse=True)
def no_backoff(monkeypatch: pytest.MonkeyPatch) -> None:
    """Retries without the wait between them: the tests count attempts, not
    seconds."""
    monkeypatch.setattr(_retry, "backoff", lambda attempt: 0.0)


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


def times(
    n: int, status: int, body: str, match: Callable[[StorageOp], bool]
) -> Callable[[StorageOp], tuple[int, str] | None]:
    """A fault answering the first ``n`` matching requests with ``status``."""
    left = [n]
    lock = threading.Lock()

    def fault(op: StorageOp) -> tuple[int, str] | None:
        with lock:
            if not match(op) or left[0] == 0:
                return None
            left[0] -= 1
            return status, body

    return fault


def puts(op: StorageOp) -> bool:
    return op.method == PUT


def gets(op: StorageOp) -> bool:
    return op.method == GET


def second_part(op: StorageOp) -> bool:
    return op.method == PUT and SECOND_PART in op.path


def body_of(size: int) -> bytes:
    return (b"paladin " * (size // 8 + 1))[:size]


def procedures(fake: FakePaladin) -> list[str]:
    return [r.procedure for r in fake.requests()]


def put_small(fake: FakePaladin) -> types_pb2.Object:
    return upload(
        fake.connect().data,
        parent=str(fake.collection()),
        content_type=CONTENT_TYPE,
        body=SMALL,
        size=len(SMALL),
    )


# ─── One PUT ────────────────────────────────────────────────────────────────


def test_an_expired_upload_url_is_presigned_again(fake: FakePaladin) -> None:
    fake.fail_storage(times(1, FORBIDDEN, EXPIRED_BODY, puts))
    obj = put_small(fake)
    assert fake.content(obj.name) == SMALL
    assert procedures(fake).count(REGENERATE) == 1


def test_a_put_whose_answer_was_lost_completes(fake: FakePaladin) -> None:
    """The first PUT stored the bytes and its answer was lost: the retry meets
    the URL's If-None-Match as 412, which means done, not failed."""
    fake.fail_storage_after_storing(times(1, UNAVAILABLE, "", puts))
    obj = put_small(fake)
    assert fake.content(obj.name) == SMALL
    assert [op.method for op in fake.storage_ops()] == [PUT, PUT]


def test_a_permanent_refusal_is_not_retried(fake: FakePaladin) -> None:
    fake.fail_storage(times(DEFAULT_TRANSFER_ATTEMPTS, BAD_REQUEST, "bad digest", puts))
    with pytest.raises(TransferError) as err:
        put_small(fake)
    assert err.value.status == BAD_REQUEST
    assert len(fake.storage_ops()) == 1


def test_retries_stop_at_the_transfers_attempts(fake: FakePaladin) -> None:
    attempts = 2
    fake.fail_storage(times(attempts + 1, UNAVAILABLE, "", puts))
    p = fake.connect(transfer=Transfer(attempts=attempts))
    with pytest.raises(TransferError):
        upload(
            p.data,
            parent=str(fake.collection()),
            content_type=CONTENT_TYPE,
            body=SMALL,
            size=len(SMALL),
        )
    assert len(fake.storage_ops()) == attempts


def test_an_upload_of_no_bytes(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = upload(p.data, parent=str(fake.collection()), content_type=CONTENT_TYPE, body=b"", size=0)
    assert fake.content(obj.name) == b""


# ─── Multipart ──────────────────────────────────────────────────────────────


def test_a_failed_part_is_retried_through_a_fresh_url(fake: FakePaladin) -> None:
    body = body_of(MULTIPART_SIZE)
    fake.fail_storage(times(1, UNAVAILABLE, "", second_part))
    obj = upload(
        fake.connect().data,
        parent=str(fake.collection()),
        content_type=CONTENT_TYPE,
        body=body,
        size=len(body),
        multipart_threshold=PART_SIZE,
    )
    assert fake.content(obj.name) == body
    # One presign per part, and one more for the retried part.
    assert procedures(fake).count(PRESIGN_PART) == MULTIPART_SIZE // PART_SIZE + 2


def test_an_interrupted_upload_resumes_with_the_parts_it_lacks(fake: FakePaladin) -> None:
    body = body_of(MULTIPART_SIZE)
    p = fake.connect()
    sessions: list[UploadSession] = []
    fake.fail_storage(times(DEFAULT_TRANSFER_ATTEMPTS, BAD_REQUEST, "refused", second_part))
    with pytest.raises(TransferError):
        upload(
            p.data,
            parent=str(fake.collection()),
            content_type=CONTENT_TYPE,
            body=body,
            size=len(body),
            multipart_threshold=PART_SIZE,
            part_concurrency=1,
            on_session=sessions.append,
        )
    # With on_session set, a failure leaves the session for a restart.
    assert ABORT not in procedures(fake)
    [session] = sessions
    fake.fail_storage(None)
    sent_before = len(fake.storage_ops())
    obj = upload(
        p.data,
        parent=str(fake.collection()),
        content_type=CONTENT_TYPE,
        body=body,
        size=len(body),
        multipart_threshold=PART_SIZE,
        resume=session,
    )
    assert obj.name == session.object_name
    assert fake.content(obj.name) == body
    resent = fake.storage_ops()[sent_before:]
    # Part 1 was stored; only parts 2 and 3 are sent again.
    assert sorted(op.path.rsplit("=", 1)[1] for op in resent) == ["2", "3"]


def test_without_on_session_a_failed_upload_is_aborted(fake: FakePaladin) -> None:
    body = body_of(MULTIPART_SIZE)
    fake.fail_storage(times(1, BAD_REQUEST, "refused", second_part))
    with pytest.raises(TransferError):
        upload(
            fake.connect().data,
            parent=str(fake.collection()),
            content_type=CONTENT_TYPE,
            body=body,
            size=len(body),
            multipart_threshold=PART_SIZE,
        )
    assert ABORT in procedures(fake)


@pytest.mark.parametrize(
    ("stored", "holds"),
    [
        (types_pb2.PartInfo(size_bytes=3, etag="5cc0f2d8dd0e7d7e4d0b1a5e1e1b1d4c"), False),
        (types_pb2.PartInfo(size_bytes=3, etag=workflows.hashlib.md5(b"abc").hexdigest()), True),
        (types_pb2.PartInfo(size_bytes=4, etag=workflows.hashlib.md5(b"abc").hexdigest()), False),
        (types_pb2.PartInfo(size_bytes=3, etag="kms-etag"), False),
        (None, False),
    ],
    ids=["other bytes", "the same bytes", "other length", "unreadable etag", "absent"],
)
def test_a_stored_part_is_kept_only_when_it_is_the_one_to_send(
    stored: types_pb2.PartInfo | None, holds: bool
) -> None:
    assert workflows._holds(stored, b"abc") is holds


# ─── Download ───────────────────────────────────────────────────────────────


def test_an_expired_download_url_is_requested_again(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, SMALL)
    fake.fail_storage(times(1, FORBIDDEN, EXPIRED_BODY, gets))
    assert download(fake.connect().data, obj.name) == SMALL
    assert procedures(fake).count(DOWNLOAD) == 2


def test_an_object_replaced_between_attempts_is_reported(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, SMALL)

    def replace(op: StorageOp) -> tuple[int, str] | None:
        # The object changes under the download, then storage fails.
        fake._commit(fake._objects[obj.name], b"replaced", "")
        fake.fail_storage(None)
        return UNAVAILABLE, ""

    fake.fail_storage(replace)
    with pytest.raises(ObjectChangedError):
        download(fake.connect().data, obj.name)


# ─── asyncio ────────────────────────────────────────────────────────────────


def test_async_upload_retries_and_completes_a_stored_put(fake: FakePaladin) -> None:
    fake.fail_storage_after_storing(times(1, UNAVAILABLE, "", puts))

    async def run() -> types_pb2.Object:
        ap = paladin.connect_async(paladin.Endpoints(data=fake.url))
        return await aupload(
            ap.data,
            parent=str(fake.collection()),
            content_type=CONTENT_TYPE,
            body=SMALL,
            size=len(SMALL),
        )

    obj = asyncio.run(run())
    assert fake.content(obj.name) == SMALL


def test_async_upload_resumes(fake: FakePaladin) -> None:
    body = body_of(MULTIPART_SIZE)
    sessions: list[UploadSession] = []
    fake.fail_storage(times(DEFAULT_TRANSFER_ATTEMPTS, BAD_REQUEST, "refused", second_part))

    async def run(resume: UploadSession | None) -> types_pb2.Object:
        ap = paladin.connect_async(paladin.Endpoints(data=fake.url))
        return await aupload(
            ap.data,
            parent=str(fake.collection()),
            content_type=CONTENT_TYPE,
            body=body,
            size=len(body),
            multipart_threshold=PART_SIZE,
            part_concurrency=1,
            on_session=sessions.append,
            resume=resume,
        )

    with pytest.raises(TransferError):
        asyncio.run(run(None))
    assert ABORT not in procedures(fake)
    fake.fail_storage(None)
    obj = asyncio.run(run(sessions[0]))
    assert fake.content(obj.name) == body


def test_async_download_retries(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, SMALL)
    fake.fail_storage(times(1, UNAVAILABLE, "", gets))

    async def run() -> bytes:
        ap = paladin.connect_async(paladin.Endpoints(data=fake.url))
        return await adownload(ap.data, obj.name)

    assert asyncio.run(run()) == SMALL


# ─── Classification ─────────────────────────────────────────────────────────

HOST = "storage"


@pytest.mark.parametrize(
    ("err", "is_expired", "is_stored", "is_retryable"),
    [
        (TransferError(PUT, HOST, FORBIDDEN, EXPIRED_BODY), True, False, True),
        (TransferError(PUT, HOST, FORBIDDEN, "SignatureDoesNotMatch"), False, False, False),
        (TransferError(PUT, HOST, PRECONDITION_FAILED, ""), False, True, False),
        (TransferError(PUT, HOST, UNAVAILABLE, ""), False, False, True),
        (TransferError(PUT, HOST, BAD_REQUEST, ""), False, False, False),
        (ConnectionResetError(), False, False, True),
        (ValueError(), False, False, False),
    ],
    ids=["expired", "bad signature", "stored", "busy", "refused", "reset", "a bug"],
)
def test_failures_are_classified(
    err: BaseException, is_expired: bool, is_stored: bool, is_retryable: bool
) -> None:
    assert expired(err) is is_expired
    assert already_stored(err) is is_stored
    assert _retry.retryable(err) is is_retryable


NOW = 1_800_000_000.0
NOW_RFC3339 = "2027-01-15T08:00:00Z"


@pytest.mark.parametrize(
    ("expires", "usable"),
    [
        ("", True),
        ("not a time", True),
        ("2027-01-15T08:00:00Z", False),
        ("2027-01-15T08:00:29Z", False),
        ("2027-01-15T08:00:31Z", True),
    ],
    ids=["no expiry", "unreadable", "expired", "inside the skew", "past the skew"],
)
def test_a_url_near_its_expiry_is_not_sent(expires: str, usable: bool) -> None:
    signed = resource_pb2.PresignedUrl(url="http://x", expires_at_rfc3339=expires)
    assert _retry.usable(signed, NOW) is usable


def test_now_matches_its_rfc3339_form() -> None:
    """The constants above describe one instant."""
    from datetime import datetime

    assert datetime.fromisoformat(NOW_RFC3339.replace("Z", "+00:00")).timestamp() == NOW


def test_backoff_doubles_up_to_its_cap() -> None:
    assert [BACKOFF(n) for n in (1, 2, 3)] == [0.2, 0.4, 0.8]
    assert BACKOFF(64) == _retry._BACKOFF_MAX


def test_transfer_attempts_must_be_positive() -> None:
    with pytest.raises(ValueError, match="attempts"):
        Transfer(attempts=0)
