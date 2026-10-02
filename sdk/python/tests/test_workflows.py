"""pages, wait, mask, upload and download, against a fake data plane and storage."""

from __future__ import annotations

import hashlib
import io
import threading
from collections.abc import Iterator
from dataclasses import dataclass, field
from urllib.parse import parse_qs
from wsgiref.simple_server import WSGIRequestHandler, make_server

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.request import RequestContext
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
from paladin.common.v1 import pagination_pb2, resource_pb2
from paladin.data.v1 import batch_service_pb2, multipart_service_pb2, object_service_pb2, types_pb2
from paladin.data.v1.multipart_service_connect import (
    MultipartUploadServiceSync,
    MultipartUploadServiceWSGIApplication,
)
from paladin.data.v1.object_service_connect import ObjectServiceSync, ObjectServiceWSGIApplication

_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_STORAGE = "/storage"
REQUIRED_HEADER = "X-Amz-Checksum-Sha256"
REQUIRED_VALUE = "signed"
PARENT = "tenants/t/collections/c"
# Polling fast enough for a test.
FAST_POLL = 0.001


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


def etag_of(body: bytes) -> str:
    return hashlib.md5(body).hexdigest()


@dataclass
class Fake(ObjectServiceSync, MultipartUploadServiceSync):
    """A data plane whose presigned URLs point at its own /storage."""

    base: str = ""
    items: list[str] = field(default_factory=list)
    page_size: int = 1
    list_calls: int = 0
    part_size: int = 4
    completed: dict[str, str] = field(default_factory=dict)
    parts: list[types_pb2.CompletedPart] = field(default_factory=list)
    aborted: int = 0
    blobs: dict[str, bytes] = field(default_factory=dict)
    headers_seen: dict[str, str] = field(default_factory=dict)
    refuse_part: str = ""
    lock: threading.Lock = field(default_factory=threading.Lock)

    def _signed(self, path: str) -> resource_pb2.PresignedUrl:
        return resource_pb2.PresignedUrl(
            url=self.base + _STORAGE + path, required_headers={REQUIRED_HEADER: REQUIRED_VALUE}
        )

    def list_objects(
        self, request: object_service_pb2.ListObjectsRequest, ctx: RequestContext
    ) -> object_service_pb2.ListObjectsResponse:
        with self.lock:
            self.list_calls += 1
        start = int(request.page.page_token or 0)
        end = min(start + self.page_size, len(self.items))
        resp = object_service_pb2.ListObjectsResponse(
            objects=[types_pb2.Object(name=n) for n in self.items[start:end]],
            page=pagination_pb2.PageResponse(),
        )
        if end < len(self.items):
            resp.page.next_page_token = str(end)
        return resp

    def upload_object(
        self, request: object_service_pb2.UploadObjectRequest, ctx: RequestContext
    ) -> object_service_pb2.UploadObjectResponse:
        return object_service_pb2.UploadObjectResponse(
            object=types_pb2.Object(name=f"{request.parent}/objects/{request.key}"),
            upload_url=self._signed(f"/{request.key}"),
        )

    def complete_object(
        self, request: object_service_pb2.CompleteObjectRequest, ctx: RequestContext
    ) -> types_pb2.Object:
        with self.lock:
            self.completed[request.name] = request.etag
        return types_pb2.Object(name=request.name)

    def download_object(
        self, request: object_service_pb2.DownloadObjectRequest, ctx: RequestContext
    ) -> object_service_pb2.DownloadObjectResponse:
        key = request.name.rsplit("/", 1)[1]
        return object_service_pb2.DownloadObjectResponse(
            object=types_pb2.Object(name=request.name), download_url=self._signed(f"/{key}")
        )

    def initiate_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        return multipart_service_pb2.InitiateMultipartUploadResponse(
            object=types_pb2.Object(name=f"{request.parent}/objects/{request.key}"),
            upload_id="u1",
            recommended_part_size=self.part_size,
        )

    def presign_part(self, request, ctx):  # type: ignore[no-untyped-def]
        return multipart_service_pb2.PresignPartResponse(
            upload_url=self._signed(f"/mp?part={request.part_number}")
        )

    def complete_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        with self.lock:
            self.parts = list(request.parts)
        return types_pb2.Object(name=request.object_name)

    def abort_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        with self.lock:
            self.aborted += 1
        return multipart_service_pb2.AbortMultipartUploadResponse()

    def storage(self, environ, start_response):  # type: ignore[no-untyped-def]
        path = environ["PATH_INFO"][len(_STORAGE) :]
        query = environ.get("QUERY_STRING", "")
        key = f"{path}?{query}"
        with self.lock:
            if environ["REQUEST_METHOD"] == "PUT":
                self.headers_seen[key] = environ.get("HTTP_X_AMZ_CHECKSUM_SHA256", "")
                if self.refuse_part and parse_qs(query).get("part") == [self.refuse_part]:
                    start_response("503 Service Unavailable", [("Content-Type", "text/plain")])
                    return [b"SlowDown"]
                body = environ["wsgi.input"].read(int(environ.get("CONTENT_LENGTH") or 0))
                self.blobs[key] = body
                start_response("200 OK", [("ETag", f'"{etag_of(body)}"')])
                return [b""]
            blob = self.blobs.get(f"{path}?")
        if blob is None:
            start_response("404 Not Found", [])
            return [b""]
        start_response("200 OK", [("Content-Type", "application/octet-stream")])
        return [blob]


@pytest.fixture
def fake() -> Iterator[Fake]:
    f = Fake()
    apps = [ObjectServiceWSGIApplication(f), MultipartUploadServiceWSGIApplication(f)]

    def route(environ, start_response):  # type: ignore[no-untyped-def]
        if environ["PATH_INFO"].startswith(_STORAGE):
            return f.storage(environ, start_response)
        length = int(environ.get("CONTENT_LENGTH") or 0)
        environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
        for app in apps:
            if environ["PATH_INFO"].startswith(app.path + "/"):
                return app(environ, start_response)
        start_response("404 Not Found", [])
        return [b""]

    httpd = make_server(_LOOPBACK, _EPHEMERAL_PORT, route, handler_class=_Quiet)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    f.base = f"http://{_LOOPBACK}:{httpd.server_port}"
    try:
        yield f
    finally:
        httpd.shutdown()
        httpd.server_close()


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


def test_upload_refuses_an_empty_body(fake: Fake) -> None:
    with pytest.raises(ValueError):
        upload(_data(fake), parent=PARENT, content_type="text/plain", body=b"", size=0)


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
