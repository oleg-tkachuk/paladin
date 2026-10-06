"""The cookbook in examples/ runs: each recipe against paladin.testing, or —
for the ones that only configure a client — builds it."""

from __future__ import annotations

import importlib.util
import io
import sys
from pathlib import Path
from types import ModuleType
from typing import Any

import pytest

import paladin
from paladin.data.v1 import object_service_pb2
from paladin.testing import FakePaladin

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def load(name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(f"examples.{name}", EXAMPLES / f"{name}.py")
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def test_every_example_is_tested() -> None:
    tested = {
        "split_horizon",
        "mtls",
        "rotating_token",
        "bulk_ingestion",
        "resumable_multipart",
        "durable_upload",
        "streaming",
        "migrating_from_connect_json",
    }
    assert {p.stem for p in EXAMPLES.glob("*.py")} == tested


def test_split_horizon() -> None:
    assert load("split_horizon").build().data is not None


@pytest.mark.skipif(importlib.util.find_spec("httpcore") is None, reason="needs the tls extra")
def test_mtls_reads_its_files_at_start_up() -> None:
    with pytest.raises(FileNotFoundError):
        load("mtls").build()


def test_rotating_token() -> None:
    m = load("rotating_token")
    store = m.SettingsStore("first")
    source = m.StoreToken(store)
    store.rotate("second")
    assert source.token("paladin-data") == "second"
    assert m.build(store).data is not None


def test_bulk_ingestion(fake: FakePaladin) -> None:
    total, failed, async_total = load("bulk_ingestion").main(fake)
    assert (total, len(failed), async_total) == (15, 1, 15)


def test_resumable_multipart(fake: FakePaladin) -> None:
    assert load("resumable_multipart").main(fake) == (1, 3, True)


def test_durable_upload(fake: FakePaladin) -> None:
    assert load("durable_upload").main(fake) == (2, True, True)


def test_streaming(fake: FakePaladin) -> None:
    assert load("streaming").main(fake)


def test_migrating_from_connect_json(fake: FakePaladin) -> None:
    assert load("migrating_from_connect_json").main(fake)


# durable_upload against what an earlier attempt can leave at its key: each
# case is a retry after a lost answer or a crash.

DURABLE_KEY = "report.pdf"


@pytest.fixture
def durable() -> ModuleType:
    # Loaded once per test: each load is a new module, with its own classes.
    return load("durable_upload")


def _durably(m: ModuleType, fake: FakePaladin, body: bytes) -> tuple[Any, int]:
    data = fake.connect().data
    return m.upload_durably(  # type: ignore[no-any-return]
        data,
        m.SessionStore(),
        parent=str(fake.collection()),
        key=DURABLE_KEY,
        content_type="application/pdf",
        size=len(body),
        open_body=lambda: io.BytesIO(body),
        attempts=3,
    )


def _registered(m: ModuleType, fake: FakePaladin, body: bytes, *, put: bool) -> Any:
    """An attempt that died after UploadObject: PENDING, and with ``put`` its
    bytes stored as well."""
    data = fake.connect().data
    assert data is not None
    sha256 = paladin.checksum(paladin.CHECKSUM_SHA256, body)
    resp = data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.collection()),
            key=DURABLE_KEY,
            content_type="application/pdf",
            size_hint_bytes=len(body),
            checksum_value=sha256,
            metadata={m.CONTENT_SHA256: sha256},
        )
    )
    if put:
        headers = dict(resp.upload_url.required_headers)
        headers["Content-Length"] = str(len(body))
        with paladin.Transfer().stream("PUT", resp.upload_url, headers, body):
            pass
    return resp.object


def test_durable_upload_takes_the_object_an_answer_was_lost_for(
    durable: ModuleType, fake: FakePaladin
) -> None:
    body = b"quarterly report"
    first, _ = _durably(durable, fake, body)  # completed; say its answer never came back
    again, attempts = _durably(durable, fake, body)
    assert (again.name, attempts) == (first.name, 1)
    assert fake.content(again.name) == body


def test_durable_upload_completes_an_object_whose_bytes_landed(
    durable: ModuleType, fake: FakePaladin
) -> None:
    body = b"quarterly report"
    pending = _registered(durable, fake, body, put=True)
    obj, attempts = _durably(durable, fake, body)
    assert (obj.name, attempts) == (pending.name, 1)
    assert fake.content(obj.name) == body


@pytest.mark.parametrize("failed", [False, True], ids=["pending", "failed"])
def test_durable_upload_replaces_an_attempt_that_stored_nothing(
    durable: ModuleType, fake: FakePaladin, failed: bool
) -> None:
    body = b"quarterly report"
    stale = _registered(durable, fake, body, put=False)
    if failed:
        fake.mark_failed(stale.name)
    obj, attempts = _durably(durable, fake, body)
    assert obj.name != stale.name and attempts == 2
    assert fake.content(obj.name) == body


def test_durable_upload_leaves_another_object_at_its_key(
    durable: ModuleType, fake: FakePaladin
) -> None:
    _durably(durable, fake, b"last quarter")
    with pytest.raises(durable.KeyTakenError):
        _durably(durable, fake, b"this quarter")


def test_durable_upload_leaves_the_trash_alone(durable: ModuleType, fake: FakePaladin) -> None:
    body = b"quarterly report"
    obj, _ = _durably(durable, fake, body)
    data = fake.connect().data
    assert data is not None
    data.object.delete_object(
        object_service_pb2.DeleteObjectRequest(name=obj.name, resource_version=obj.resource_version)
    )
    # Restoring or purging it is not a retry's call.
    with pytest.raises(durable.KeyTakenError):
        _durably(durable, fake, body)


def test_durable_upload_needs_a_key(durable: ModuleType, fake: FakePaladin) -> None:
    m = durable
    with pytest.raises(ValueError):
        m.upload_durably(
            fake.connect().data,
            m.SessionStore(),
            parent=str(fake.collection()),
            key="",
            content_type="text/plain",
            size=1,
            open_body=lambda: io.BytesIO(b"x"),
        )
