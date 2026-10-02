"""The shared SDK scenarios (sdk/testdata/scenarios.json) against a live server —
the compose stack backend/scripts/verify-stack.sh boots, phase sdk. The Go SDK
runs the same scenarios.

    PALADIN_SDK_DATA_URL=… PALADIN_SDK_TOKEN=… PALADIN_SDK_COLLECTION=… \\
    PALADIN_SDK_CONFORMANCE=1 uv run pytest tests/conformance

Without PALADIN_SDK_CONFORMANCE the scenarios are skipped, so the ordinary
suite needs no server; with it, a missing variable is a failure.
"""

from __future__ import annotations

import json
import os
import secrets
import uuid
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import pytest

from paladin import (
    CollectionName,
    Endpoints,
    NotFoundError,
    ObjectURI,
    PaladinError,
    StaticToken,
    connect,
    download,
    download_uri,
    idempotency_key,
    pages,
    upload,
)
from paladin.common.v1 import pagination_pb2
from paladin.data.v1 import object_service_pb2

SCENARIOS = Path(__file__).resolve().parents[3] / "testdata" / "scenarios.json"
ENABLE = "PALADIN_SDK_CONFORMANCE"
ENV_URL, ENV_TOKEN, ENV_COLLECTION = (
    "PALADIN_SDK_DATA_URL",
    "PALADIN_SDK_TOKEN",
    "PALADIN_SDK_COLLECTION",
)
MULTIPART_THRESHOLD = 5 << 20
MULTIPART_SIZE = 2 * MULTIPART_THRESHOLD + 7
SMALL_SIZE = 4 << 10
MISSING_OBJECT = "00000000-0000-4000-8000-000000000000"


@dataclass
class Env:
    data: object
    collection: CollectionName


@pytest.fixture(scope="module")
def env() -> Env:
    if not os.environ.get(ENABLE):
        pytest.skip(f"set {ENABLE}=1 and a server to run the conformance scenarios")
    missing = [v for v in (ENV_URL, ENV_TOKEN, ENV_COLLECTION) if not os.environ.get(v)]
    if missing:
        pytest.fail(f"{ENABLE} is set but {', '.join(missing)} are not")
    p = connect(
        Endpoints(data=os.environ[ENV_URL]), token_source=StaticToken(os.environ[ENV_TOKEN])
    )
    return Env(p.data, CollectionName.parse(os.environ[ENV_COLLECTION]))


def _key(name: str) -> str:
    return f"sdk-conformance/python/{name}/{uuid.uuid4()}"


def _put(e: Env, key: str, body: bytes, threshold: int = 8 << 20):  # type: ignore[no-untyped-def]
    return upload(
        e.data,  # type: ignore[arg-type]
        parent=str(e.collection),
        key=key,
        content_type="application/octet-stream",
        body=body,
        size=len(body),
        multipart_threshold=threshold,
    )


def upload_small(e: Env) -> None:
    body = secrets.token_bytes(SMALL_SIZE)
    assert download(e.data, _put(e, _key("small"), body).name) == body  # type: ignore[arg-type]


def upload_multipart(e: Env) -> None:
    body = secrets.token_bytes(MULTIPART_SIZE)
    obj = _put(e, _key("multipart"), body, MULTIPART_THRESHOLD)
    assert download(e.data, obj.name) == body  # type: ignore[arg-type]


def range_read(e: Env) -> None:
    body = secrets.token_bytes(SMALL_SIZE)
    obj = _put(e, _key("range"), body)
    assert download(e.data, obj.name, offset=100, length=50) == body[100:150]  # type: ignore[arg-type]


def lookup_by_uri(e: Env) -> None:
    key, body = _key("uri"), secrets.token_bytes(SMALL_SIZE)
    _put(e, key, body)
    with download_uri(e.data, ObjectURI(e.collection, key)) as reader:  # type: ignore[arg-type]
        assert reader.read() == body


def list_pages(e: Env) -> None:
    for _ in range(3):
        _put(e, _key("list"), b"x")
    # One per page, so following the pages is what finds them all.
    request = object_service_pb2.ListObjectsRequest(
        parent=str(e.collection), page=pagination_pb2.PageRequest(page_size=1)
    )
    assert sum(1 for _ in pages(e.data.object.list_objects, request, "objects")) >= 3  # type: ignore[attr-defined]


def not_found_is_typed(e: Env) -> None:
    with pytest.raises(NotFoundError):
        e.data.object.get_object(  # type: ignore[attr-defined]
            object_service_pb2.GetObjectRequest(name=f"{e.collection}/objects/{MISSING_OBJECT}")
        )


def idempotent_replay(e: Env) -> None:
    request = object_service_pb2.UploadObjectRequest(
        parent=str(e.collection), key=_key("replay"), content_type="text/plain", size_hint_bytes=1
    )
    with idempotency_key(secrets.token_hex(16)):
        first = e.data.object.upload_object(request)  # type: ignore[attr-defined]
        again = e.data.object.upload_object(request)  # type: ignore[attr-defined]
    assert again.object.name == first.object.name


def error_names_the_release(e: Env) -> None:
    with pytest.raises(PaladinError) as err:
        e.data.object.get_object(  # type: ignore[attr-defined]
            object_service_pb2.GetObjectRequest(name=f"{e.collection}/objects/{MISSING_OBJECT}")
        )
    assert err.value.server_version


RUNNERS: dict[str, Callable[[Env], None]] = {
    f.__name__: f
    for f in (
        upload_small,
        upload_multipart,
        range_read,
        lookup_by_uri,
        list_pages,
        not_found_is_typed,
        idempotent_replay,
        error_names_the_release,
    )
}


def test_every_scenario_is_implemented() -> None:
    assert sorted(RUNNERS) == sorted(json.loads(SCENARIOS.read_text())["scenarios"])


@pytest.mark.parametrize("name", sorted(RUNNERS))
def test_scenario(env: Env, name: str) -> None:
    RUNNERS[name](env)
