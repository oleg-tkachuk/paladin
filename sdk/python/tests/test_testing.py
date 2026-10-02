"""paladin.testing, the fake consumers test against — exercised through the
SDK's own workflows, as a consumer would."""

from __future__ import annotations

import pytest

from paladin import (
    CHECKSUM_SHA256,
    ContractSkewError,
    FailedPreconditionError,
    NotFoundError,
    ObjectURI,
    download,
    download_uri,
    lookup_object,
    pages,
    upload,
)
from paladin.admin.v1 import tenant_service_pb2
from paladin.data.v1 import object_service_pb2
from paladin.testing import PART_SIZE, FakePaladin


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


@pytest.mark.parametrize(
    ("size", "threshold"),
    [(1 << 10, 8 << 20), (2 * PART_SIZE + 3, PART_SIZE)],
    ids=["one PUT", "multipart"],
)
def test_upload_and_download_through_the_fake(fake: FakePaladin, size: int, threshold: int) -> None:
    p = fake.connect()
    body = (b"paladin " * (size // 8 + 1))[:size]
    obj = upload(
        p.data,
        parent=str(fake.collection()),
        key="docs/a.pdf",
        content_type="application/pdf",
        body=body,
        size=size,
        multipart_threshold=threshold,
    )
    assert fake.content(obj.name) == body
    assert download(p.data, obj.name) == body
    uri = ObjectURI(fake.collection(), "docs/a.pdf")
    with download_uri(p.data, uri, offset=1, length=3) as reader:
        assert reader.read() == body[1:4]


def test_the_fake_records_the_checksum_download_verifies(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = upload(
        p.data, parent=str(fake.collection()), key="k", content_type="text/plain", body=b"x", size=1
    )
    assert obj.checksum.algorithm == CHECKSUM_SHA256 and obj.checksum.value


def test_put_list_and_delete(fake: FakePaladin) -> None:
    p = fake.connect()
    for key in ("c", "a", "b"):
        fake.put(fake.collection(), key, "text/plain", key.encode())
    fake.put(fake.collection("other"), "z", "text/plain", b"z")
    keys = [
        o.key
        for o in pages(
            p.data.object.list_objects,
            object_service_pb2.ListObjectsRequest(parent=str(fake.collection())),
            "objects",
        )
    ]
    assert keys == ["a", "b", "c"]
    obj = lookup_object(p.data, ObjectURI(fake.collection(), "b"))
    p.data.object.delete_object(object_service_pb2.DeleteObjectRequest(name=obj.name))
    with pytest.raises(NotFoundError):
        p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))


def test_everything_else_is_unimplemented(fake: FakePaladin) -> None:
    with pytest.raises(ContractSkewError):
        fake.connect().admin.tenant.list_tenants(tenant_service_pb2.ListTenantsRequest())


def test_the_fake_refuses_what_the_server_refuses(fake: FakePaladin) -> None:
    p = fake.connect()
    up = p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(parent=str(fake.collection()), key="k")
    )
    with pytest.raises(FailedPreconditionError):
        p.data.object.complete_object(
            object_service_pb2.CompleteObjectRequest(name=up.object.name, etag="x")
        )
