"""pages, wait, mask, upload and download, against a fake data plane and storage."""

from __future__ import annotations

import io

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from data_plane_fake import PARENT, REQUIRED_VALUE, Fake, etag_of
from google.rpc import code_pb2, status_pb2

from paladin import (
    Endpoints,
    OperationFailed,
    TransferError,
    connect,
    download,
    mask,
    pages,
    upload,
    wait,
)
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.data.v1 import batch_service_pb2, object_service_pb2, types_pb2

# Polling fast enough for a test.
FAST_POLL = 0.001


def _data(fake: Fake):  # type: ignore[no-untyped-def]
    return connect(Endpoints(data=fake.base)).data


def test_pages_follows_every_page(fake: Fake) -> None:
    fake.items, fake.page_size = ["a", "b", "c", "d", "e"], 2
    got = [
        o.name
        for o in pages(
            _data(fake).object.list_objects,
            object_service_pb2.ListObjectsRequest(parent=PARENT),
            "objects",
        )
    ]
    assert got == ["a", "b", "c", "d", "e"]
    assert fake.list_calls == 3


def test_pages_stops_when_the_loop_does(fake: Fake) -> None:
    fake.items = ["a", "b", "c"]
    next(pages(_data(fake).object.list_objects, object_service_pb2.ListObjectsRequest(), "objects"))
    assert fake.list_calls == 1


def test_pages_refuses_an_unpaged_message() -> None:
    with pytest.raises(TypeError):
        list(pages(lambda req: types_pb2.Object(), object_service_pb2.GetObjectRequest(), "tags"))


def _ops(*ops: batch_service_pb2.Operation):  # type: ignore[no-untyped-def]
    calls = {"n": 0}

    def get() -> batch_service_pb2.Operation:
        op = ops[min(calls["n"], len(ops) - 1)]
        calls["n"] += 1
        return op

    return get, calls


def test_wait_polls_until_done() -> None:
    get, calls = _ops(
        batch_service_pb2.Operation(name="op"), batch_service_pb2.Operation(name="op", done=True)
    )
    assert wait(get, poll=FAST_POLL).done
    assert calls["n"] == 2


def test_wait_raises_the_operations_error() -> None:
    failed = batch_service_pb2.Operation(
        name="op",
        done=True,
        error=status_pb2.Status(code=code_pb2.FAILED_PRECONDITION, message="not empty"),
    )
    get, _ = _ops(failed)
    with pytest.raises(OperationFailed) as err:
        wait(get, poll=FAST_POLL)
    assert err.value.code == Code.FAILED_PRECONDITION


def test_wait_times_out() -> None:
    get, _ = _ops(batch_service_pb2.Operation(name="op"))
    with pytest.raises(TimeoutError):
        wait(get, poll=FAST_POLL, timeout=0.02)


@pytest.mark.parametrize(
    ("paths", "ok"),
    [
        (("filter", "sink", "disabled"), True),
        (("sink.http.url",), True),
        (("filtr",), False),
        (("filter.x",), False),
        (("sink.http.uri",), False),
    ],
)
def test_mask(paths: tuple[str, ...], ok: bool) -> None:
    if ok:
        assert list(mask(admin_types.EventSubscription, *paths).paths) == list(paths)
    else:
        with pytest.raises(ValueError):
            mask(admin_types.EventSubscription, *paths)


def test_upload_and_download_a_small_object(fake: Fake) -> None:
    body = b"hello, paladin"
    obj = upload(
        _data(fake),
        parent=PARENT,
        key="k.txt",
        content_type="text/plain",
        body=body,
        size=len(body),
    )
    assert fake.completed[obj.name] == etag_of(body)
    assert fake.headers_seen["/k.txt?"] == REQUIRED_VALUE
    assert download(_data(fake), obj.name) == body


def test_upload_splits_a_large_object_into_parts(fake: Fake) -> None:
    body = io.BytesIO(b"0123456789")
    upload(
        _data(fake),
        parent=PARENT,
        key="big.bin",
        content_type="application/octet-stream",
        body=body,
        size=10,
        multipart_threshold=8,
    )
    chunks = [b"0123", b"4567", b"89"]
    assert [(p.part_number, p.etag) for p in fake.parts] == [
        (i + 1, etag_of(c)) for i, c in enumerate(chunks)
    ]
    assert [fake.blobs[f"/mp?part={i + 1}"] for i in range(3)] == chunks
    assert fake.aborted == 0


def test_upload_aborts_when_a_part_is_refused(fake: Fake) -> None:
    fake.refuse_part = "2"
    with pytest.raises(TransferError) as err:
        upload(
            _data(fake),
            parent=PARENT,
            key="big.bin",
            content_type="application/octet-stream",
            body=b"0123456789",
            size=10,
            multipart_threshold=8,
        )
    assert err.value.status == 503
    assert fake.aborted == 1 and not fake.parts


def test_upload_refuses_a_negative_size(fake: Fake) -> None:
    with pytest.raises(ValueError):
        upload(_data(fake), parent=PARENT, content_type="text/plain", body=b"", size=-1)


def test_rpc_errors_pass_through(fake: Fake) -> None:
    with pytest.raises(ConnectError):
        _data(fake).object.get_object(object_service_pb2.GetObjectRequest(name="x"))


@pytest.mark.parametrize(
    ("number", "code"),
    [
        (code_pb2.CANCELLED, Code.CANCELED),
        (code_pb2.NOT_FOUND, Code.NOT_FOUND),
        (999, Code.UNKNOWN),
    ],
)
def test_operation_failed_maps_rpc_codes(number: int, code: Code) -> None:
    op = batch_service_pb2.Operation(name="op", done=True, error=status_pb2.Status(code=number))
    assert OperationFailed(op).code == code
