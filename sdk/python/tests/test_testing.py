"""paladin.testing, the fake consumers test against — exercised through the
SDK's own workflows, as a consumer would."""

from __future__ import annotations

import urllib.request

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin import (
    CHECKSUM_SHA256,
    HEADER_AUTHORIZATION,
    HEADER_IDEMPOTENCY_KEY,
    HEADER_USER_AGENT,
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
from paladin.data.v1 import object_service_pb2, storage_bootstrap_service_pb2, types_pb2
from paladin.testing import PART_SIZE, FakePaladin

UPLOAD_PROCEDURE = "/paladin.data.v1.ObjectService/UploadObject"
BACKEND = "seaweedfs"
BUCKET = "paladin-shared"


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


def _put(url: str, body: bytes) -> None:
    request = urllib.request.Request(url, data=body, method="PUT")
    with urllib.request.urlopen(request) as response:  # the fake's own loopback URL
        response.read()


def test_complete_matches_the_server(fake: FakePaladin) -> None:
    p = fake.connect()
    body = b"no etag"
    up = p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(parent=str(fake.collection()), key="k")
    )
    _put(up.upload_url.url, body)
    # The ETag is optional, as on the server.
    obj = p.data.object.complete_object(
        object_service_pb2.CompleteObjectRequest(name=up.object.name)
    )
    assert obj.state == types_pb2.OBJECT_STATE_AVAILABLE
    # Completing it again returns it, whatever the ETag.
    again = p.data.object.complete_object(
        object_service_pb2.CompleteObjectRequest(name=up.object.name, etag="stale")
    )
    assert again.etag == obj.etag
    assert fake.content(up.object.name) == body


def test_ensure_tenant_storage(fake: FakePaladin) -> None:
    p = fake.connect()

    def ensure(*collections: str) -> storage_bootstrap_service_pb2.EnsureTenantStorageResponse:
        return p.data.storage_bootstrap.ensure_tenant_storage(
            storage_bootstrap_service_pb2.EnsureTenantStorageRequest(
                backend_id=BACKEND, bucket=BUCKET, collections=collections
            )
        )

    first = ensure("a", "b")
    assert first.bucket_created
    assert list(first.collections_created) == ["a", "b"]
    assert not first.collections_existing
    second = ensure("b", "c")
    assert not second.bucket_created
    assert list(second.collections_created) == ["c"]
    assert list(second.collections_existing) == ["b"]
    with pytest.raises(ConnectError) as err:
        p.data.storage_bootstrap.ensure_tenant_storage(
            storage_bootstrap_service_pb2.EnsureTenantStorageRequest(
                backend_id=BACKEND, bucket="ab"
            )
        )
    assert err.value.code == Code.INVALID_ARGUMENT


def test_requests_show_what_the_client_sent(fake: FakePaladin) -> None:
    token = "test-token"
    p = fake.connect(bearer_token=token)
    p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(parent=str(fake.collection()), key="k")
    )
    [request] = fake.requests()
    assert request.procedure == UPLOAD_PROCEDURE
    assert request.headers[HEADER_AUTHORIZATION.lower()] == f"Bearer {token}"
    assert request.headers[HEADER_IDEMPOTENCY_KEY.lower()]
    assert request.headers[HEADER_USER_AGENT.lower()].startswith("paladin-sdk-python/")
