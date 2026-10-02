"""A data plane whose presigned URLs point at its own /storage: the fake that
upload, download and the paging and waiting workflows run against."""

from __future__ import annotations

import hashlib
import io
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field
from urllib.parse import parse_qs
from wsgiref.simple_server import WSGIRequestHandler, make_server

from connectrpc.request import RequestContext

from paladin.common.v1 import pagination_pb2, resource_pb2
from paladin.data.v1 import multipart_service_pb2, object_service_pb2, types_pb2
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
    checksums: dict[str, str] = field(default_factory=dict)
    hosts: list[str] = field(default_factory=list)
    # storage requests that carried a traceparent
    traceparents: int = 0
    refuse_part: str = ""
    ignore_range: bool = False
    redirect_to: str = ""
    refuse_with: tuple[int, bytes] | None = None
    # What download_object says of the object; None: its name alone.
    described: types_pb2.Object | None = None
    no_url: bool = False
    # parent|key of every lookup_object
    looked_up: list[str] = field(default_factory=list)
    # The origin presigned URLs are signed for; "" is base.
    signed_origin: str = ""
    lock: threading.Lock = field(default_factory=threading.Lock)

    def _signed(self, path: str) -> resource_pb2.PresignedUrl:
        return resource_pb2.PresignedUrl(
            url=(self.signed_origin or self.base) + _STORAGE + path,
            required_headers={REQUIRED_HEADER: REQUIRED_VALUE},
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
            self.checksums[request.name] = request.checksum_value
        return types_pb2.Object(name=request.name)

    def download_object(
        self, request: object_service_pb2.DownloadObjectRequest, ctx: RequestContext
    ) -> object_service_pb2.DownloadObjectResponse:
        key = request.name.rsplit("/", 1)[1]
        resp = object_service_pb2.DownloadObjectResponse(
            object=self.described or types_pb2.Object(name=request.name),
            download_url=self._signed(f"/{key}"),
        )
        if self.no_url:
            resp.ClearField("download_url")
        return resp

    def lookup_object(
        self, request: object_service_pb2.LookupObjectRequest, ctx: RequestContext
    ) -> types_pb2.Object:
        with self.lock:
            self.looked_up.append(f"{request.parent}|{request.key}")
        return types_pb2.Object(name=f"{request.parent}/objects/{request.key}")

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
            self.hosts.append(environ.get("HTTP_HOST", ""))
            if environ.get("HTTP_TRACEPARENT"):
                self.traceparents += 1
            if self.redirect_to:
                start_response("307 Temporary Redirect", [("Location", self.redirect_to)])
                return [b""]
            if self.refuse_with:
                status, body = self.refuse_with
                start_response(f"{status} Refused", [("Content-Type", "text/plain")])
                return [body]
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
        ranged = environ.get("HTTP_RANGE", "")
        if ranged and not self.ignore_range:
            first, _, last = ranged.removeprefix("bytes=").partition("-")
            end = int(last) + 1 if last else len(blob)
            part = blob[int(first) : end]
            start_response(
                "206 Partial Content",
                [("Content-Type", "application/octet-stream"), ("Content-Length", str(len(part)))],
            )
            return [part]
        start_response(
            "200 OK",
            [("Content-Type", "application/octet-stream"), ("Content-Length", str(len(blob)))],
        )
        return [blob]


@contextmanager
def serving() -> Iterator[Fake]:
    """A Fake served on loopback, its storage beside its services."""
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
