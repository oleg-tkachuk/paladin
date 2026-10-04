"""The async workflows and the bulk downloads, against paladin.testing."""

from __future__ import annotations

import asyncio
import threading
import time

import pytest

from paladin import (
    Endpoints,
    IntegrityError,
    NotFoundError,
    ObjectURI,
    Transfer,
    TransferError,
    UploadItem,
    adownload,
    adownload_many,
    adownload_uri,
    aupload,
    aupload_many,
    connect_async,
    download_many,
    upload_many,
    workflows,
)
from paladin.testing import PART_SIZE, FakePaladin, StorageOp

# Long enough that downloads started together overlap.
OVERLAP = 0.05


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def _adata(fake: FakePaladin):  # type: ignore[no-untyped-def]
    return connect_async(Endpoints(data=fake.url)).data


@pytest.mark.parametrize(
    ("size", "threshold"),
    [(1 << 10, 8 << 20), (2 * PART_SIZE + 3, PART_SIZE)],
    ids=["one PUT", "multipart"],
)
def test_async_upload_and_download(fake: FakePaladin, size: int, threshold: int) -> None:
    body = (b"paladin " * (size // 8 + 1))[:size]

    async def run() -> None:
        data = _adata(fake)
        obj = await aupload(
            data,
            parent=str(fake.collection()),
            key="a.pdf",
            content_type="application/pdf",
            body=body,
            size=size,
            multipart_threshold=threshold,
        )
        assert fake.content(obj.name) == body
        assert await adownload(data, obj.name) == body
        reader = await adownload_uri(
            data, ObjectURI(fake.collection(), "a.pdf"), offset=2, length=3
        )
        async with reader:
            assert await reader.read() == body[2:5]

    asyncio.run(run())


def test_async_download_verifies(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "k", "text/plain", b"content")
    obj.checksum.algorithm, obj.checksum.value = (
        "SHA256",
        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    )

    async def run() -> None:
        with pytest.raises(IntegrityError):
            await adownload(_adata(fake), obj.name)

    asyncio.run(run())


def test_download_many_yields_every_object_and_every_failure(fake: FakePaladin) -> None:
    names = [
        fake.put(fake.collection(), f"k{i}", "text/plain", f"body {i}".encode()).name
        for i in range(5)
    ]
    missing = f"{fake.collection()}/objects/00000000-0000-4000-8000-000000000000"
    got = dict(download_many(fake.connect().data, [*names, missing], concurrency=2))
    assert {n: got[n] for n in names} == {n: f"body {i}".encode() for i, n in enumerate(names)}
    assert isinstance(got[missing], NotFoundError)


def test_download_many_keeps_to_its_concurrency(monkeypatch: pytest.MonkeyPatch) -> None:
    active, peak, lock = 0, 0, threading.Lock()

    def slow(data, name):  # type: ignore[no-untyped-def]
        nonlocal active, peak
        with lock:
            active += 1
            peak = max(peak, active)
        time.sleep(OVERLAP)
        with lock:
            active -= 1
        return name.encode()

    monkeypatch.setattr(workflows, "download", slow)
    got = list(download_many(object(), [str(i) for i in range(9)], concurrency=3))  # type: ignore[arg-type]
    assert len(got) == 9 and peak == 3


def test_adownload_many(fake: FakePaladin) -> None:
    names = [fake.put(fake.collection(), f"k{i}", "text/plain", bytes([i])).name for i in range(4)]

    async def run() -> dict[str, object]:
        return {n: b async for n, b in adownload_many(_adata(fake), names, concurrency=2)}

    assert asyncio.run(run()) == {n: bytes([i]) for i, n in enumerate(names)}


def test_adownload_many_keeps_to_its_concurrency(monkeypatch: pytest.MonkeyPatch) -> None:
    active, peak = 0, 0

    async def slow(data, name):  # type: ignore[no-untyped-def]
        nonlocal active, peak
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(OVERLAP)
        active -= 1
        return name.encode()

    monkeypatch.setattr(workflows, "adownload", slow)

    async def run() -> int:
        return len(
            [x async for x in adownload_many(object(), [str(i) for i in range(7)], concurrency=3)]
        )  # type: ignore[arg-type]

    assert asyncio.run(run()) == 7 and peak == 3


@pytest.mark.parametrize("bad", [0, -1])
def test_bulk_concurrency_must_be_positive(bad: int) -> None:
    with pytest.raises(ValueError):
        list(download_many(object(), ["a"], concurrency=bad))  # type: ignore[arg-type]


def _item(fake: FakePaladin, key: str, body: bytes) -> UploadItem:
    return UploadItem(
        parent=str(fake.collection()), content_type="text/plain", body=body, size=len(body), key=key
    )


def test_upload_many_yields_every_object_and_every_failure(fake: FakePaladin) -> None:
    items = [_item(fake, f"k{i}", f"body {i}".encode()) for i in range(5)]
    short = UploadItem(parent=str(fake.collection()), content_type="text/plain", body=b"x", size=2)
    got = dict(upload_many(fake.connect().data, [*items, short], concurrency=2))
    for i, item in enumerate(items):
        assert fake.content(got[item].name) == f"body {i}".encode()  # type: ignore[union-attr]
    assert isinstance(got[short], ValueError)


def test_upload_many_aborts_only_the_multipart_upload_that_failed(fake: FakePaladin) -> None:
    body = b"x" * (PART_SIZE + 1)  # two parts
    puts = 0

    def refuse_after_two(op: StorageOp) -> tuple[int, str] | None:
        # One at a time, so the first upload's parts are the first two PUTs.
        nonlocal puts
        if op.method == "PUT":
            puts += 1
            if puts > 2:
                return 403, "refused"
        return None

    fake.fail_storage(refuse_after_two)
    good, bad = _item(fake, "good", body), _item(fake, "bad", body)
    data = fake.connect(transfer=Transfer(attempts=1)).data
    got = dict(upload_many(data, [good, bad], concurrency=1, multipart_threshold=PART_SIZE))
    assert fake.content(got[good].name) == body  # type: ignore[union-attr]
    assert isinstance(got[bad], TransferError)
    aborts = [r for r in fake.requests() if r.procedure.endswith("/AbortMultipartUpload")]
    assert len(aborts) == 1


def test_upload_many_draws_items_only_as_a_slot_frees(monkeypatch: pytest.MonkeyPatch) -> None:
    active, peak, drawn, lock = 0, 0, 0, threading.Lock()

    def slow(data, **kwargs):  # type: ignore[no-untyped-def]
        nonlocal active, peak
        with lock:
            active += 1
            peak = max(peak, active)
        time.sleep(OVERLAP)
        with lock:
            active -= 1
        return kwargs["key"]

    def items():  # type: ignore[no-untyped-def]
        nonlocal drawn
        for i in range(9):
            drawn += 1
            # Never more drawn than finished plus in flight.
            assert drawn <= done + 3
            yield UploadItem(parent="p", content_type="", body=b"", size=0, key=str(i))

    monkeypatch.setattr(workflows, "upload", slow)
    done = 0
    for _ in upload_many(object(), items(), concurrency=3):  # type: ignore[arg-type]
        done += 1
    assert done == 9 and peak == 3


def test_aupload_many(fake: FakePaladin) -> None:
    items = [_item(fake, f"k{i}", bytes([i])) for i in range(4)]

    async def run() -> dict[UploadItem, object]:
        return {i: o async for i, o in aupload_many(_adata(fake), items, concurrency=2)}

    got = asyncio.run(run())
    assert [fake.content(got[i].name) for i in items] == [bytes([n]) for n in range(4)]  # type: ignore[attr-defined]


def test_aupload_many_keeps_to_its_concurrency(monkeypatch: pytest.MonkeyPatch) -> None:
    active, peak = 0, 0

    async def slow(data, **kwargs):  # type: ignore[no-untyped-def]
        nonlocal active, peak
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(OVERLAP)
        active -= 1
        return kwargs["key"]

    monkeypatch.setattr(workflows, "aupload", slow)
    items = [
        UploadItem(parent="p", content_type="", body=b"", size=0, key=str(i)) for i in range(7)
    ]

    async def run() -> int:
        return len([x async for x in aupload_many(object(), items, concurrency=3)])  # type: ignore[arg-type]

    assert asyncio.run(run()) == 7 and peak == 3


def test_upload_many_refuses_no_concurrency() -> None:
    with pytest.raises(ValueError):
        list(upload_many(object(), [], concurrency=0))  # type: ignore[arg-type]
