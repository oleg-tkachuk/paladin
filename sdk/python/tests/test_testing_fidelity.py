"""The fake refuses what the server refuses: requests its contract's rules
refuse, a stale resource_version, list parameters it does not apply."""

from __future__ import annotations

import sys
from collections.abc import Callable, Iterator

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

import paladin
from paladin import IntegrityError, InvalidArgumentError, VersionConflictError, download
from paladin.common.v1 import error_reason_pb2, pagination_pb2, resource_pb2
from paladin.data.v1 import object_service_pb2
from paladin.testing import PART_SIZE, TESTING_EXTRA_HINT, FakePaladin, StorageOp

CONTENT_TYPE = "application/octet-stream"
"""A test upload's content type: the server refuses an upload that names none."""
VALIDATION_FAILED = "validation failed"
"""How the server's answer to a request its contract's rules refuse begins."""
BODY = b"x"


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


def _next_version(version: str) -> str:
    """The resource_version after one more change to the row."""
    return str(int(version) + 1)


def _validation_cases(fake: FakePaladin) -> dict[str, Callable[[], object]]:
    data = fake.connect().data
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, BODY)
    return {
        "get_object with no name": lambda: data.object.get_object(  # type: ignore[union-attr]
            object_service_pb2.GetObjectRequest()
        ),
        "upload_object with no content type": lambda: data.object.upload_object(  # type: ignore[union-attr]
            object_service_pb2.UploadObjectRequest(
                parent=str(fake.collection()), key="new", size_hint_bytes=1
            )
        ),
        "delete_object with no resource_version": lambda: data.object.delete_object(  # type: ignore[union-attr]
            object_service_pb2.DeleteObjectRequest(name=obj.name)
        ),
    }


@pytest.mark.parametrize(
    "case",
    [
        "get_object with no name",
        "upload_object with no content type",
        "delete_object with no resource_version",
    ],
)
def test_the_fake_validates_every_request(fake: FakePaladin, case: str) -> None:
    with pytest.raises(InvalidArgumentError) as caught:
        _validation_cases(fake)[case]()
    assert VALIDATION_FAILED in str(caught.value)


@pytest.mark.parametrize(
    ("field", "request_fields"),
    [
        ("filter", {"filter": 'key = "a"'}),
        ("order_by", {"order_by": "key"}),
        ("sort_order", {"sort_order": pagination_pb2.SORT_ORDER_DESC}),
    ],
)
def test_list_objects_refuses_what_it_does_not_apply(
    fake: FakePaladin, field: str, request_fields: dict[str, object]
) -> None:
    data = fake.connect().data
    with pytest.raises(ConnectError) as caught:
        data.object.list_objects(  # type: ignore[union-attr]
            object_service_pb2.ListObjectsRequest(parent=str(fake.collection()), **request_fields)
        )
    assert caught.value.code == Code.UNIMPLEMENTED
    assert field in str(caught.value)


def test_a_plain_listing_is_served(fake: FakePaladin) -> None:
    fake.put(fake.collection(), "k", CONTENT_TYPE, BODY)
    listed = fake.connect().data.object.list_objects(  # type: ignore[union-attr]
        object_service_pb2.ListObjectsRequest(parent=str(fake.collection()))
    )
    assert [o.key for o in listed.objects] == ["k"]


@pytest.mark.parametrize("permanent", [False, True])
@pytest.mark.parametrize(
    ("version", "error", "reason"),
    [
        (_next_version, VersionConflictError, error_reason_pb2.ERROR_REASON_VERSION_CONFLICT),
        (lambda _: "v1", InvalidArgumentError, error_reason_pb2.ERROR_REASON_INVALID_ARGUMENT),
    ],
    ids=["a stale version", "not a version"],
)
def test_delete_object_checks_the_version(
    fake: FakePaladin,
    permanent: bool,
    version: Callable[[str], str],
    error: type[Exception],
    reason: int,
) -> None:
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, BODY)
    with pytest.raises(error) as caught:
        fake.connect().data.object.delete_object(  # type: ignore[union-attr]
            object_service_pb2.DeleteObjectRequest(
                name=obj.name, resource_version=version(obj.resource_version), permanent=permanent
            )
        )
    assert caught.value.reason == reason  # type: ignore[attr-defined]
    assert fake.content(obj.name) == BODY, "a refused delete removed the object"


@pytest.mark.parametrize("permanent", [False, True])
def test_delete_object_at_the_current_version(fake: FakePaladin, permanent: bool) -> None:
    obj = fake.put(fake.collection(), "k", CONTENT_TYPE, BODY)
    fake.connect().data.object.delete_object(  # type: ignore[union-attr]
        object_service_pb2.DeleteObjectRequest(
            name=obj.name, resource_version=obj.resource_version, permanent=permanent
        )
    )
    assert fake.content(obj.name) is None


def test_a_change_advances_the_version(fake: FakePaladin) -> None:
    data = fake.connect().data
    registered = data.object.upload_object(  # type: ignore[union-attr]
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.collection()),
            key="k",
            content_type=CONTENT_TYPE,
            size_hint_bytes=len(BODY),
            checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
            checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, BODY),
        )
    ).object
    fake.mark_failed(registered.name)
    got = data.object.get_object(  # type: ignore[union-attr]
        object_service_pb2.GetObjectRequest(name=registered.name)
    )
    assert got.resource_version == _next_version(registered.resource_version)


def test_the_fake_without_the_extra_says_what_to_install(monkeypatch: pytest.MonkeyPatch) -> None:
    # A None entry makes the import fail as an absent package does.
    monkeypatch.setitem(sys.modules, "protovalidate", None)
    with pytest.raises(ImportError) as caught:
        FakePaladin()
    assert str(caught.value) == TESTING_EXTRA_HINT


def test_a_multipart_download_is_verified(fake: FakePaladin) -> None:
    data = fake.connect().data
    body = b"p" * (2 * PART_SIZE + 1)  # three parts
    obj = paladin.upload(
        data,
        parent=str(fake.collection()),
        key="big",
        content_type=CONTENT_TYPE,
        body=body,
        size=len(body),
        multipart_threshold=PART_SIZE,
    )
    assert obj.checksum.part_size_bytes == PART_SIZE and obj.checksum.value.endswith("-3")
    assert download(data, obj.name) == body
    # Storage answers with other bytes of the same length: only the
    # composite tells them apart.
    corrupt = "q" * len(body)

    def other_bytes(op: StorageOp) -> tuple[int, str] | None:
        return (200, corrupt) if op.method == "GET" else None

    fake.fail_storage(other_bytes)
    with pytest.raises(IntegrityError) as caught:
        download(data, obj.name)
    assert caught.value.what == paladin.CHECKSUM_SHA256
