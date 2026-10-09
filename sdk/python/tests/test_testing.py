"""paladin.testing, the fake consumers test against — exercised through the
SDK's own workflows, as a consumer would."""

from __future__ import annotations

import urllib.error
import urllib.request
from collections.abc import Mapping
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from http import HTTPStatus

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf import duration_pb2

import paladin
from paladin import (
    CHECKSUM_SHA256,
    HEADER_AUTHORIZATION,
    HEADER_IDEMPOTENCY_KEY,
    HEADER_USER_AGENT,
    ContractSkewError,
    FailedPreconditionError,
    NotFoundError,
    ObjectName,
    ObjectURI,
    Retry,
    VersionConflictError,
    download,
    download_uri,
    lookup_object,
    pages,
    upload,
)
from paladin.admin.v1 import tenant_service_pb2
from paladin.common.v1 import error_reason_pb2, resource_pb2
from paladin.data.v1 import (
    multipart_service_pb2,
    object_service_pb2,
    presign_service_pb2,
    storage_bootstrap_service_pb2,
    types_pb2,
)
from paladin.testing import (
    DEFAULT_DOWNLOAD_TTL,
    MAX_PRESIGN_TTL,
    PART_SIZE,
    FakePaladin,
)

# The content type of a test upload: the server refuses an upload that names none.
CONTENT_TYPE = "application/octet-stream"

UPLOAD_PROCEDURE = "/paladin.data.v1.ObjectService/UploadObject"
GET_OBJECT_PROCEDURE = "/paladin.data.v1.ObjectService/GetObject"
COUNT_OBJECTS_PROCEDURE = "/paladin.data.v1.ObjectService/CountObjects"
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
    p.data.object.delete_object(
        object_service_pb2.DeleteObjectRequest(name=obj.name, resource_version=obj.resource_version)
    )
    with pytest.raises(NotFoundError):
        p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))


def test_everything_else_is_unimplemented(fake: FakePaladin) -> None:
    with pytest.raises(ContractSkewError):
        fake.connect().admin.tenant.list_tenants(tenant_service_pb2.ListTenantsRequest())


def test_the_fake_refuses_what_the_server_refuses(fake: FakePaladin) -> None:
    p = fake.connect()
    # An upload with no checksum to bind its URL to.
    with pytest.raises(ConnectError):
        p.data.object.upload_object(
            object_service_pb2.UploadObjectRequest(
                content_type=CONTENT_TYPE, parent=str(fake.collection()), key="k"
            )
        )
    up = p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            content_type=CONTENT_TYPE,
            parent=str(fake.collection()),
            key="k",
            checksum_value=EMPTY_SHA256,
        )
    )
    with pytest.raises(FailedPreconditionError):
        p.data.object.complete_object(
            object_service_pb2.CompleteObjectRequest(name=up.object.name, etag="x")
        )


EMPTY_SHA256 = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
"""The SHA-256 of an empty body, for uploads whose content is not sent."""


def _put(url: str, body: bytes, headers: Mapping[str, str] | None = None) -> int:
    request = urllib.request.Request(url, data=body, method="PUT", headers=dict(headers or {}))
    try:
        with urllib.request.urlopen(request) as response:  # the fake's own loopback URL
            response.read()
            return int(response.status)
    except urllib.error.HTTPError as err:
        return int(err.code)


def test_complete_matches_the_server(fake: FakePaladin) -> None:
    p = fake.connect()
    body = b"no etag"
    up = p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            content_type=CONTENT_TYPE,
            parent=str(fake.collection()),
            key="k",
            size_hint_bytes=len(body),
            checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, body),
        )
    )
    assert _put(up.upload_url.url, body, up.upload_url.required_headers) == HTTP_OK
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
        object_service_pb2.UploadObjectRequest(
            content_type=CONTENT_TYPE,
            parent=str(fake.collection()),
            key="k",
            checksum_value=EMPTY_SHA256,
        )
    )
    [request] = fake.requests()
    assert request.procedure == UPLOAD_PROCEDURE
    assert request.headers[HEADER_AUTHORIZATION.lower()] == f"Bearer {token}"
    assert request.headers[HEADER_IDEMPOTENCY_KEY.lower()]
    assert request.headers[HEADER_USER_AGENT.lower()].startswith("paladin-sdk-python/")


HTTP_OK = 200
HTTP_BAD_REQUEST = 400
HTTP_PRECONDITION_FAILED = 412


def test_the_fake_storage_enforces_the_binding(fake: FakePaladin) -> None:
    """The fake's storage holds a URL to its binding, as the object store
    does: another body, a missing signed header, or an overwrite is refused."""
    p = fake.connect()
    body = b"bound body"
    up = p.data.object.upload_object(
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.collection()),
            key="k",
            content_type="text/plain",
            size_hint_bytes=len(body),
            checksum_value=paladin.checksum(paladin.CHECKSUM_SHA256, body),
        )
    )
    signed = dict(up.upload_url.required_headers)
    assert _put(up.upload_url.url, b"other body", signed) == HTTP_BAD_REQUEST
    missing = {k: v for k, v in signed.items() if k != "X-Amz-Checksum-Sha256"}
    assert _put(up.upload_url.url, body, missing) == HTTP_BAD_REQUEST
    assert _put(up.upload_url.url, body, signed) == HTTP_OK
    assert _put(up.upload_url.url, body, signed) == HTTP_PRECONDITION_FAILED


def test_the_fake_honours_if_match(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    resp = p.data.object.download_object(
        object_service_pb2.DownloadObjectRequest(name=obj.name, require_etag_match=True)
    )
    etag = resp.download_url.required_headers["If-Match"]
    assert etag

    def get(if_match: str) -> int:
        request = urllib.request.Request(resp.download_url.url, headers={"If-Match": if_match})
        try:
            with urllib.request.urlopen(request) as response:  # the fake's own loopback URL
                response.read()
                return int(response.status)
        except urllib.error.HTTPError as err:
            return int(err.code)

    assert get(etag) == HTTP_OK
    assert get('"stale"') == HTTP_PRECONDITION_FAILED


def test_the_fake_holds_one_object_per_key(fake: FakePaladin) -> None:
    # As the server's unique path: in any state, the trash included, until a
    # permanent delete frees it.
    data = fake.connect().data

    def put() -> types_pb2.Object:
        return upload(
            data,  # type: ignore[arg-type]
            parent=str(fake.collection()),
            key="k",
            content_type="text/plain",
            body=b"x",
            size=1,
            metadata={"m": "v"},
        )

    obj = put()
    assert obj.metadata["m"] == "v"
    with pytest.raises(paladin.AlreadyExistsError):
        put()
    with pytest.raises(paladin.AlreadyExistsError):
        data.multipart_upload.initiate_multipart_upload(  # type: ignore[union-attr]
            multipart_service_pb2.InitiateMultipartUploadRequest(
                checksum_algorithm=resource_pb2.CHECKSUM_ALGORITHM_SHA256,
                content_type=CONTENT_TYPE,
                parent=str(fake.collection()),
                key="k",
                size_bytes=1,
            )
        )

    def delete(version: str, permanent: bool) -> None:
        data.object.delete_object(  # type: ignore[union-attr]
            object_service_pb2.DeleteObjectRequest(
                name=obj.name, resource_version=version, permanent=permanent
            )
        )

    delete(obj.resource_version, False)
    with pytest.raises(paladin.AlreadyExistsError):
        put()
    # The soft delete advanced the version by one, as the server's trigger
    # does on every change to the row.
    delete(str(int(obj.resource_version) + 1), True)
    put()


def test_the_fake_looks_up_and_fails_pending_objects(fake: FakePaladin) -> None:
    data = fake.connect().data
    pending = data.object.upload_object(  # type: ignore[union-attr]
        object_service_pb2.UploadObjectRequest(
            parent=str(fake.collection()),
            key="k",
            content_type="text/plain",
            size_hint_bytes=1,
            checksum_value=paladin.checksum(CHECKSUM_SHA256, b"x"),
        )
    ).object

    def state() -> int:
        return lookup_object(data, ObjectURI(fake.collection(), "k")).state  # type: ignore[arg-type]

    # The server finds an object in any state but DELETED.
    assert state() == types_pb2.OBJECT_STATE_PENDING
    fake.mark_failed(pending.name)
    assert state() == types_pb2.OBJECT_STATE_FAILED


# ─── fail_rpc ───────────────────────────────────────────────────────────────


def _get_object(p: paladin.Paladin, obj: types_pb2.Object) -> None:
    p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))


def _calls(fake: FakePaladin, procedure: str) -> int:
    return sum(1 for r in fake.requests() if r.procedure == procedure)


@pytest.mark.parametrize(
    ("times", "code", "kind", "want_reason"),
    [
        (1, Code.UNAVAILABLE, None, error_reason_pb2.ERROR_REASON_UNSPECIFIED),
        (1, Code.NOT_FOUND, NotFoundError, error_reason_pb2.ERROR_REASON_NOT_FOUND),
        (3, Code.UNAVAILABLE, None, error_reason_pb2.ERROR_REASON_UNSPECIFIED),
        (1, Code.DATA_LOSS, None, error_reason_pb2.ERROR_REASON_UNSPECIFIED),
        (1, Code.ABORTED, VersionConflictError, error_reason_pb2.ERROR_REASON_VERSION_CONFLICT),
    ],
    ids=["one Unavailable", "one NotFound", "three Unavailable", "DataLoss", "Aborted"],
)
def test_fail_rpc(
    fake: FakePaladin,
    times: int,
    code: Code,
    kind: type[Exception] | None,
    want_reason: int,
) -> None:
    """The next calls answer the code with the reason the server attaches to
    it, each is recorded, and then the RPC is served again."""
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    fake.fail_rpc(GET_OBJECT_PROCEDURE, times, code)
    for _ in range(times):
        with pytest.raises(ConnectError) as caught:
            _get_object(p, obj)
        assert caught.value.code == code
        if kind is not None:
            assert isinstance(caught.value, kind)
        assert paladin.reason(caught.value) == want_reason
    _get_object(p, obj)
    assert _calls(fake, GET_OBJECT_PROCEDURE) == times + 1


def test_fail_rpc_sees_the_retry(fake: FakePaladin) -> None:
    """A client that retries UNAVAILABLE reads through one failure, and the
    fake shows both attempts."""
    p = fake.connect(retry=Retry(attempts=3, base_delay=0.001, max_delay=0.001))
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    fake.fail_rpc(GET_OBJECT_PROCEDURE, 1, Code.UNAVAILABLE)
    _get_object(p, obj)
    assert _calls(fake, GET_OBJECT_PROCEDURE) == 2


def test_fail_rpc_fails_only_its_procedure(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    fake.fail_rpc(GET_OBJECT_PROCEDURE, 1, Code.UNAVAILABLE)
    lookup_object(p.data, ObjectURI(fake.collection(), "k"))
    with pytest.raises(ConnectError) as caught:
        _get_object(p, obj)
    assert caught.value.code == Code.UNAVAILABLE


def test_fail_rpc_reset_restores_the_rpc(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    reset = fake.fail_rpc(GET_OBJECT_PROCEDURE, 5, Code.UNAVAILABLE)
    with pytest.raises(ConnectError):
        _get_object(p, obj)
    reset()
    _get_object(p, obj)
    reset()  # twice is harmless


def test_fail_rpc_reset_leaves_a_later_failure(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    first = fake.fail_rpc(GET_OBJECT_PROCEDURE, 1, Code.UNAVAILABLE)
    fake.fail_rpc(GET_OBJECT_PROCEDURE, 1, Code.NOT_FOUND)
    first()
    with pytest.raises(NotFoundError):
        _get_object(p, obj)


def test_fail_rpc_on_an_unimplemented_procedure(fake: FakePaladin) -> None:
    """Any procedure of a served service can fail, one the fake answers
    UNIMPLEMENTED included."""
    p = fake.connect()
    fake.fail_rpc(COUNT_OBJECTS_PROCEDURE, 1, Code.UNAVAILABLE)
    with pytest.raises(ConnectError) as caught:
        p.data.object.count_objects(
            object_service_pb2.CountObjectsRequest(parent=str(fake.collection()))
        )
    assert caught.value.code == Code.UNAVAILABLE
    with pytest.raises(ContractSkewError):
        p.data.object.count_objects(
            object_service_pb2.CountObjectsRequest(parent=str(fake.collection()))
        )


@pytest.mark.parametrize(
    ("procedure", "times", "code", "error"),
    [
        ("/paladin.data.v1.ObjectService/NoSuchRPC", 1, Code.UNAVAILABLE, ValueError),
        ("/paladin.admin.v1.TenantService/ListTenants", 1, Code.UNAVAILABLE, ValueError),
        ("GetObject", 1, Code.UNAVAILABLE, ValueError),
        (GET_OBJECT_PROCEDURE, 0, Code.UNAVAILABLE, ValueError),
        (GET_OBJECT_PROCEDURE, 1, "unavailable", TypeError),
    ],
    ids=[
        "no such procedure",
        "a service not served",
        "not a procedure",
        "zero times",
        "not a Code",
    ],
)
def test_fail_rpc_refuses_a_mistake(
    fake: FakePaladin, procedure: str, times: int, code: Code, error: type[Exception]
) -> None:
    with pytest.raises(error):
        fake.fail_rpc(procedure, times, code)


# ─── presign_download ───────────────────────────────────────────────────────


def _presign(p: paladin.Paladin, **fields: object) -> presign_service_pb2.PresignDownloadResponse:
    return p.data.presign.presign_download(presign_service_pb2.PresignDownloadRequest(**fields))


def _fetch(url: object, headers: Mapping[str, str]) -> tuple[int, bytes, Mapping[str, str]]:
    request = urllib.request.Request(url.url, method=url.method, headers=dict(headers))  # type: ignore[attr-defined]
    try:
        with urllib.request.urlopen(request) as resp:
            return resp.status, resp.read(), resp.headers
    except urllib.error.HTTPError as err:
        return err.code, b"", err.headers


def _expires(url: object) -> datetime:
    return datetime.strptime(url.expires_at_rfc3339, "%Y-%m-%dT%H:%M:%SZ").replace(  # type: ignore[attr-defined]
        tzinfo=UTC
    )


def test_presign_download_reads_the_object(fake: FakePaladin) -> None:
    p = fake.connect()
    body = b"presigned body"
    obj = fake.put(fake.collection(), "k", "text/plain", body)
    before = datetime.now(UTC)
    url = _presign(p, name=obj.name).download_url
    assert url.method == "GET"
    assert not url.required_headers
    # RFC 3339 here has whole seconds: allow one either way.
    second = timedelta(seconds=1)
    expires = _expires(url)
    assert before + DEFAULT_DOWNLOAD_TTL - second <= expires
    assert expires <= datetime.now(UTC) + DEFAULT_DOWNLOAD_TTL + second
    status, got, _ = _fetch(url, url.required_headers)
    assert (status, got) == (HTTPStatus.OK, body)


def test_presign_download_honours_the_request(fake: FakePaladin) -> None:
    ttl = timedelta(hours=1)
    disposition = 'attachment; filename="a.txt"'
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    duration = duration_pb2.Duration()
    duration.FromTimedelta(ttl)
    url = _presign(
        p,
        name=obj.name,
        ttl=duration,
        content_disposition=disposition,
        require_etag_match=True,
    ).download_url
    left = _expires(url) - datetime.now(UTC)
    assert ttl - timedelta(minutes=1) <= left <= ttl + timedelta(seconds=1)
    assert url.required_headers["If-Match"] == f'"{obj.etag}"'
    status, _, headers = _fetch(url, url.required_headers)
    assert status == HTTPStatus.OK
    assert headers["Content-Disposition"] == disposition
    stale, _, _ = _fetch(url, {"If-Match": '"stale"'})
    assert stale == HTTPStatus.PRECONDITION_FAILED


def test_presign_download_refuses_what_the_server_refuses(fake: FakePaladin) -> None:
    p = fake.connect()
    available = fake.put(fake.collection(), "available", "text/plain", b"x")

    def register(key: str) -> str:
        return p.data.object.upload_object(
            object_service_pb2.UploadObjectRequest(
                content_type=CONTENT_TYPE,
                parent=str(fake.collection()),
                key=key,
                checksum_value=EMPTY_SHA256,
            )
        ).object.name

    pending = register("pending")
    failed = register("failed")
    fake.mark_failed(failed)
    unknown = str(ObjectName(fake.collection(), "00000000-0000-0000-0000-000000000000"))

    def ttl(delta: timedelta) -> duration_pb2.Duration:
        d = duration_pb2.Duration()
        d.FromTimedelta(delta)
        return d

    cases = [
        ({"name": pending}, Code.FAILED_PRECONDITION),
        ({"name": failed}, Code.FAILED_PRECONDITION),
        ({"name": unknown}, Code.NOT_FOUND),
        ({"name": "objects/x"}, Code.INVALID_ARGUMENT),
        ({"name": available.name, "ttl": ttl(-timedelta(seconds=1))}, Code.INVALID_ARGUMENT),
        (
            {"name": available.name, "ttl": ttl(MAX_PRESIGN_TTL + timedelta(seconds=1))},
            Code.INVALID_ARGUMENT,
        ),
    ]
    for fields, code in cases:
        with pytest.raises(ConnectError) as caught:
            _presign(p, **fields)
        assert caught.value.code == code, fields


def test_the_fake_serves_concurrent_callers(fake: FakePaladin) -> None:
    """Callers sharing one fake call at once — more of them than the standard
    library's server queues — and every call is served."""
    callers = 32
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")

    def call(_: int) -> str:
        return p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name)).name

    with ThreadPoolExecutor(callers) as pool:
        assert list(pool.map(call, range(callers))) == [obj.name] * callers
