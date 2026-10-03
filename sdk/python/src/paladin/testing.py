"""An in-memory Paladin data plane for the tests of a program built on the SDK.

    with paladin.testing.FakePaladin() as fake:
        p = fake.connect()
        obj = paladin.upload(p.data, parent=str(fake.collection()), …)

It serves ObjectService (upload, complete, get, lookup, list, download,
delete), MultipartUploadService, ListParts included, and
StorageBootstrapService, with presigned URLs on its own storage; every other
RPC answers Unimplemented, as a server that lacks it does. Like the server it
binds every upload URL to the size and checksum the upload was registered
with — its storage refuses a PUT without exactly the signed headers, a body of
another length or SHA-256, or an overwrite — records that checksum on the
object, so a download verifies what it reads, and it answers Range and
If-Match requests. ``requests()`` lists the RPCs it
received, with their headers. It runs on the standard library's WSGI server,
in a thread, on loopback.
"""

from __future__ import annotations

import base64
import hashlib
import io
import json
import threading
import uuid
from dataclasses import dataclass, field
from types import TracebackType
from typing import TYPE_CHECKING, Any
from urllib.parse import parse_qs
from wsgiref.simple_server import WSGIRequestHandler, WSGIServer, make_server

from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin.common.v1 import pagination_pb2, resource_pb2
from paladin.connect import Endpoints, Paladin, connect
from paladin.data.v1 import (
    multipart_service_pb2,
    object_service_pb2,
    storage_bootstrap_service_pb2,
    types_pb2,
)
from paladin.data.v1.multipart_service_connect import (
    MultipartUploadServiceSync,
    MultipartUploadServiceWSGIApplication,
)
from paladin.data.v1.object_service_connect import ObjectServiceSync, ObjectServiceWSGIApplication
from paladin.data.v1.storage_bootstrap_service_connect import (
    StorageBootstrapServiceSync,
    StorageBootstrapServiceWSGIApplication,
)
from paladin.names import CollectionName, InvalidNameError
from paladin.transfer import CHECKSUM_SHA256

if TYPE_CHECKING:  # annotations only: typing.Self is 3.11+
    from typing_extensions import Self

DEFAULT_COLLECTION = "default"
"""The collection ``collection()`` names."""
PART_SIZE = 5 << 20
"""The part size multipart uploads are told to use."""
DEFAULT_PAGE_SIZE = 100
"""The page ``list_objects`` answers when asked for none."""

_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_STORAGE = "/storage/"
_PART = "part"
_OBJECTS_SEP = "/objects/"
_PUT = "PUT"
_GET = "GET"
_AVAILABLE = types_pb2.OBJECT_STATE_AVAILABLE
_PENDING = types_pb2.OBJECT_STATE_PENDING
_DELETED = types_pb2.OBJECT_STATE_DELETED
# The WSGI environ names a request header HTTP_<NAME>, with _ for -.
_HEADER_PREFIX = "HTTP_"
# Bounds of a bucket name, as the contract sets them.
_MIN_BUCKET_LEN = 3
_MAX_BUCKET_LEN = 63
# Headers a bound upload URL requires, as the real presigner signs them.
_CONTENT_LENGTH = "Content-Length"
_CONTENT_TYPE = "Content-Type"
_IF_NONE_MATCH = "If-None-Match"
_IF_MATCH = "If-Match"
_IF_NONE_MATCH_ANY = "*"
_CHECKSUM_SHA256_HEADER = "X-Amz-Checksum-Sha256"
# A base64 SHA-256 digest is 44 characters.
_SHA256_B64_LEN = 44


def _sha256(body: bytes) -> str:
    return base64.b64encode(hashlib.sha256(body).digest()).decode()


@dataclass(frozen=True)
class _Binding:
    """What one URL accepts: the body's exact length and SHA-256 and, for a
    whole object, its Content-Type and no overwrite."""

    size: int
    checksum: str
    content_type: str = ""
    no_overwrite: bool = False

    def headers(self) -> dict[str, str]:
        h = {_CONTENT_LENGTH: str(self.size), _CHECKSUM_SHA256_HEADER: self.checksum}
        if self.content_type:
            h[_CONTENT_TYPE] = self.content_type
        if self.no_overwrite:
            h[_IF_NONE_MATCH] = _IF_NONE_MATCH_ANY
        return h

    def refuses(self, environ: dict[str, Any], body: bytes) -> str:
        """Why a PUT breaks the binding; "" when it keeps it."""
        for name, want in self.headers().items():
            got = environ.get(_environ_key(name), "")
            if got != want:
                return f"signed header {name} is {got!r}, want {want!r}"
        if len(body) != self.size:
            return f"body is {len(body)} bytes, signed for {self.size}"
        if _sha256(body) != self.checksum:
            return "body's SHA-256 is not the signed one"
        return ""


def _environ_key(header: str) -> str:
    """Where WSGI puts a request header."""
    upper = header.upper().replace("-", "_")
    return upper if upper in ("CONTENT_LENGTH", "CONTENT_TYPE") else _HEADER_PREFIX + upper


def _require_checksum(value: str) -> None:
    if len(value) != _SHA256_B64_LEN:
        raise ConnectError(Code.INVALID_ARGUMENT, "checksum_value must be a base64 SHA-256")


def _etag(body: bytes) -> str:
    return hashlib.md5(body, usedforsecurity=False).hexdigest()


@dataclass
class _Object:
    msg: types_pb2.Object
    body: bytes | None = None
    put: bytes | None = None
    bound: _Binding | None = None


@dataclass(frozen=True)
class Request:
    """One RPC the fake received."""

    procedure: str
    """The RPC, ``/paladin.data.v1.ObjectService/GetObject``."""
    headers: dict[str, str]
    """What the client sent, by lower-case name: credentials, the idempotency
    key, the User-Agent."""


@dataclass
class _Multipart:
    name: str
    size: int
    parts: dict[int, bytes] = field(default_factory=dict)
    bindings: dict[int, _Binding] = field(default_factory=dict)


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


class FakePaladin(ObjectServiceSync, MultipartUploadServiceSync, StorageBootstrapServiceSync):
    """The fake; a context manager that starts and stops it. Thread-safe."""

    def __init__(self) -> None:
        self.tenant = str(uuid.uuid4())
        """The id of the fake's one tenant."""
        self.url = ""
        """The base URL, for every plane; set once started."""
        self._lock = threading.Lock()
        self._objects: dict[str, _Object] = {}
        self._uploads: dict[str, _Multipart] = {}
        self._buckets: set[tuple[str, str]] = set()
        self._bound: set[str] = set()
        self._requests: list[Request] = []
        self._httpd: WSGIServer | None = None

    # ─── Lifecycle ──────────────────────────────────────────────────────────

    def __enter__(self) -> Self:
        apps = [
            ObjectServiceWSGIApplication(self),
            MultipartUploadServiceWSGIApplication(self),
            StorageBootstrapServiceWSGIApplication(self),
        ]

        def route(environ, start_response):  # type: ignore[no-untyped-def]
            if environ["PATH_INFO"].startswith(_STORAGE):
                return self._storage(environ, start_response)
            # wsgiref hands over the raw socket, which blocks past the body.
            length = int(environ.get("CONTENT_LENGTH") or 0)
            environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
            for app in apps:
                if environ["PATH_INFO"].startswith(app.path + "/"):
                    self._record(environ)
                    return app(environ, start_response)
            return _unimplemented(start_response)

        self._httpd = make_server(_LOOPBACK, _EPHEMERAL_PORT, route, handler_class=_Quiet)
        threading.Thread(target=self._httpd.serve_forever, daemon=True).start()
        self.url = f"http://{_LOOPBACK}:{self._httpd.server_port}"
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        value: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        if self._httpd is not None:
            self._httpd.shutdown()
            self._httpd.server_close()

    def connect(self, **client_options: Any) -> Paladin:
        """Clients for every plane, all served by the fake."""
        return connect(Endpoints(data=self.url, admin=self.url, iam=self.url), **client_options)

    def collection(self, name: str = DEFAULT_COLLECTION) -> CollectionName:
        """A collection in the fake's tenant; every collection exists."""
        return CollectionName(self.tenant, name)

    # ─── Inspection ─────────────────────────────────────────────────────────

    def put(
        self, collection: CollectionName, key: str, content_type: str, body: bytes
    ) -> types_pb2.Object:
        """Store an object directly, as if uploaded and completed."""
        with self._lock:
            o = self._new(str(collection), key, content_type)
            self._commit(o, body, "")
            return o.msg

    def requests(self) -> list[Request]:
        """The RPCs received, oldest first; one the fake does not serve is not
        among them."""
        with self._lock:
            return [Request(r.procedure, dict(r.headers)) for r in self._requests]

    def _record(self, environ: dict[str, Any]) -> None:
        headers = {
            key[len(_HEADER_PREFIX) :].replace("_", "-").lower(): value
            for key, value in environ.items()
            if key.startswith(_HEADER_PREFIX)
        }
        # WSGI keeps these two out of the HTTP_ names.
        for key in ("CONTENT_TYPE", "CONTENT_LENGTH"):
            if environ.get(key):
                headers[key.replace("_", "-").lower()] = environ[key]
        with self._lock:
            self._requests.append(Request(environ["PATH_INFO"], headers))

    def content(self, name: str) -> bytes | None:
        """What an object holds; None when there is no such object."""
        with self._lock:
            o = self._objects.get(name)
            return o.body if o is not None and o.msg.state == _AVAILABLE else None

    # ─── Helpers ────────────────────────────────────────────────────────────

    def _new(self, parent: str, key: str, content_type: str) -> _Object:
        object_id = str(uuid.uuid4())
        msg = types_pb2.Object(
            name=f"{parent}{_OBJECTS_SEP}{object_id}",
            object_id=object_id,
            tenant_id=self.tenant,
            collection=CollectionName.parse(parent).collection,
            key=key or object_id,
            content_type=content_type,
            state=_PENDING,
        )
        o = _Object(msg)
        self._objects[msg.name] = o
        return o

    @staticmethod
    def _commit(o: _Object, body: bytes, checksum: str) -> None:
        o.body = body
        o.msg.etag = _etag(body)
        o.msg.size_bytes = len(body)
        o.msg.state = _AVAILABLE
        if checksum:
            o.msg.checksum.algorithm = CHECKSUM_SHA256
            o.msg.checksum.value = checksum

    def _signed(self, path: str, method: str) -> resource_pb2.PresignedUrl:
        return resource_pb2.PresignedUrl(url=f"{self.url}{_STORAGE}{path}", method=method)

    def _get(self, name: str) -> _Object:
        o = self._objects.get(name)
        if o is None or o.msg.state == _DELETED:
            raise ConnectError(Code.NOT_FOUND, f"{name} not found")
        return o

    @staticmethod
    def _parent(parent: str) -> None:
        try:
            CollectionName.parse(parent)
        except InvalidNameError as err:
            raise ConnectError(Code.INVALID_ARGUMENT, str(err)) from err

    def _by_id(self, object_id: str) -> _Object | None:
        return next((o for o in self._objects.values() if o.msg.object_id == object_id), None)

    # ─── ObjectService ──────────────────────────────────────────────────────

    def upload_object(self, request, ctx):  # type: ignore[no-untyped-def]
        self._parent(request.parent)
        _require_checksum(request.checksum_value)
        with self._lock:
            o = self._new(request.parent, request.key, request.content_type)
            o.bound = _Binding(
                request.size_hint_bytes, request.checksum_value, request.content_type, True
            )
            url = self._signed(o.msg.object_id, _PUT)
            url.required_headers.update(o.bound.headers())
            return object_service_pb2.UploadObjectResponse(object=o.msg, upload_url=url)

    def complete_object(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            o = self._objects.get(request.name)
            if o is None:
                raise ConnectError(Code.NOT_FOUND, f"{request.name} not found")
            # As the server: completing a completed object returns it.
            if o.msg.state == _AVAILABLE:
                return o.msg
            if o.put is None:
                raise ConnectError(Code.FAILED_PRECONDITION, "nothing was uploaded")
            # The ETag is optional; one that is given must be the content's.
            if request.etag and request.etag != _etag(o.put):
                raise ConnectError(
                    Code.FAILED_PRECONDITION, "the ETag is not the uploaded content's"
                )
            registered = o.bound.checksum if o.bound else ""
            if request.checksum_value and request.checksum_value != registered:
                raise ConnectError(
                    Code.FAILED_PRECONDITION, "checksum_value differs from the registered one"
                )
            self._commit(o, o.put, registered)
            return o.msg

    def get_object(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            return self._get(request.name).msg

    def lookup_object(self, request, ctx):  # type: ignore[no-untyped-def]
        prefix = request.parent + _OBJECTS_SEP
        with self._lock:
            for o in self._objects.values():
                if (
                    o.msg.name.startswith(prefix)
                    and o.msg.key == request.key
                    and o.msg.state == _AVAILABLE
                ):
                    return o.msg
        raise ConnectError(Code.NOT_FOUND, f"{request.parent} has no key {request.key!r}")

    def list_objects(self, request, ctx):  # type: ignore[no-untyped-def]
        prefix = request.parent + _OBJECTS_SEP
        with self._lock:
            found = sorted(
                (
                    o.msg
                    for o in self._objects.values()
                    if o.msg.name.startswith(prefix) and o.msg.state == _AVAILABLE
                ),
                key=lambda m: m.key,
            )
        try:
            start = int(request.page.page_token or 0)
        except ValueError as err:
            raise ConnectError(Code.INVALID_ARGUMENT, "bad page token") from err
        end = min(start + (request.page.page_size or DEFAULT_PAGE_SIZE), len(found))
        resp = object_service_pb2.ListObjectsResponse(
            objects=found[start:end], page=pagination_pb2.PageResponse()
        )
        if end < len(found):
            resp.page.next_page_token = str(end)
        return resp

    def download_object(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            o = self._get(request.name)
            if o.msg.state != _AVAILABLE:
                raise ConnectError(Code.FAILED_PRECONDITION, "the object is not complete")
            url = self._signed(o.msg.object_id, _GET)
            if request.require_etag_match:
                url.required_headers[_IF_MATCH] = f'"{o.msg.etag}"'
            return object_service_pb2.DownloadObjectResponse(object=o.msg, download_url=url)

    def delete_object(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            o = self._get(request.name)
            o.msg.state = _DELETED
            o.body = None
            return object_service_pb2.DeleteObjectResponse()

    # ─── MultipartUploadService ─────────────────────────────────────────────

    def initiate_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        self._parent(request.parent)
        if request.size_bytes <= 0:
            raise ConnectError(Code.INVALID_ARGUMENT, "size_bytes must be positive")
        with self._lock:
            o = self._new(request.parent, request.key, request.content_type)
            upload_id = str(uuid.uuid4())
            self._uploads[upload_id] = _Multipart(o.msg.name, request.size_bytes)
            return multipart_service_pb2.InitiateMultipartUploadResponse(
                object=o.msg,
                upload_id=upload_id,
                recommended_part_size=PART_SIZE,
                total_parts=-(-request.size_bytes // PART_SIZE),
            )

    def presign_part(self, request, ctx):  # type: ignore[no-untyped-def]
        _require_checksum(request.checksum_value)
        with self._lock:
            up = self._uploads.get(request.upload_id)
            if up is None:
                raise ConnectError(Code.NOT_FOUND, f"upload {request.upload_id} not found")
            total = -(-up.size // PART_SIZE)
            n = request.part_number
            if not 1 <= n <= total:
                raise ConnectError(Code.INVALID_ARGUMENT, f"part {n} out of range 1..{total}")
            length = PART_SIZE if n < total else up.size - (total - 1) * PART_SIZE
            up.bindings[n] = _Binding(length, request.checksum_value)
            url = self._signed(f"{request.upload_id}?{_PART}={n}", _PUT)
            url.required_headers.update(up.bindings[n].headers())
        return multipart_service_pb2.PresignPartResponse(upload_url=url)

    def complete_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            up = self._uploads.get(request.upload_id)
            if up is None:
                raise ConnectError(Code.NOT_FOUND, f"upload {request.upload_id} not found")
            body = bytearray()
            for i, part in enumerate(request.parts):
                data = up.parts.get(part.part_number)
                bound = up.bindings.get(part.part_number)
                if (
                    data is None
                    or part.part_number != i + 1
                    or part.etag != _etag(data)
                    or bound is None
                    or part.checksum_value != bound.checksum
                ):
                    raise ConnectError(
                        Code.FAILED_PRECONDITION, f"part {part.part_number} is not the uploaded one"
                    )
                body += data
            o = self._objects[up.name]
            self._commit(o, bytes(body), "")
            del self._uploads[request.upload_id]
            return o.msg

    def list_parts(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            up = self._uploads.get(request.upload_id)
            if up is None:
                raise ConnectError(Code.NOT_FOUND, f"upload {request.upload_id} not found")
            return multipart_service_pb2.ListPartsResponse(
                parts=[
                    types_pb2.PartInfo(part_number=n, size_bytes=len(data), etag=_etag(data))
                    for n, data in sorted(up.parts.items())
                ],
                page=pagination_pb2.PageResponse(),
            )

    def abort_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        with self._lock:
            up = self._uploads.pop(request.upload_id, None)
            if up is not None:
                self._objects.pop(up.name, None)
        return multipart_service_pb2.AbortMultipartUploadResponse()

    # ─── StorageBootstrapService ────────────────────────────────────────────

    def ensure_tenant_storage(self, request, ctx):  # type: ignore[no-untyped-def]
        """Records the bucket and the collections, and reports which this call
        created. Any backend id is taken to exist; every collection already
        works for objects, bootstrapped or not."""
        if not request.backend_id or not (
            _MIN_BUCKET_LEN <= len(request.bucket) <= _MAX_BUCKET_LEN
        ):
            raise ConnectError(
                Code.INVALID_ARGUMENT,
                "a backend id and a bucket of 3 to 63 characters are required",
            )
        resp = storage_bootstrap_service_pb2.EnsureTenantStorageResponse()
        with self._lock:
            bucket = (request.backend_id, request.bucket)
            if bucket not in self._buckets:
                self._buckets.add(bucket)
                resp.bucket_created = True
            for c in request.collections:
                if c in self._bound:
                    resp.collections_existing.append(c)
                else:
                    self._bound.add(c)
                    resp.collections_created.append(c)
        return resp

    # ─── Storage ────────────────────────────────────────────────────────────

    def _storage(self, environ, start_response):  # type: ignore[no-untyped-def]
        object_id = environ["PATH_INFO"][len(_STORAGE) :]
        query = parse_qs(environ.get("QUERY_STRING", ""))
        method = environ["REQUEST_METHOD"]
        if method == _PUT:
            body = environ["wsgi.input"].read(int(environ.get("CONTENT_LENGTH") or 0))
            with self._lock:
                if _PART in query:
                    up = self._uploads.get(object_id)
                    if up is None:
                        return _status(start_response, "404 Not Found")
                    n = int(query[_PART][0])
                    bound = up.bindings.get(n)
                    if bound is None:
                        return _status(start_response, "403 Forbidden")
                    if bound.refuses(environ, body):
                        return _status(start_response, "400 Bad Request")
                    up.parts[n] = body
                else:
                    o = self._by_id(object_id)
                    if o is None:
                        return _status(start_response, "404 Not Found")
                    # If-None-Match: * — the key already holds a stored object.
                    if o.put is not None or o.msg.state == _AVAILABLE:
                        return _status(start_response, "412 Precondition Failed")
                    if o.bound is None or o.bound.refuses(environ, body):
                        return _status(start_response, "400 Bad Request")
                    o.put = body
            start_response("200 OK", [("ETag", f'"{_etag(body)}"'), ("Content-Length", "0")])
            return [b""]
        if method != _GET:
            return _status(start_response, "405 Method Not Allowed")
        with self._lock:
            o = self._by_id(object_id)
            content = o.body if o is not None else None
            etag = f'"{o.msg.etag}"' if o is not None else ""
        if content is None:
            return _status(start_response, "404 Not Found")
        if_match = environ.get(_environ_key(_IF_MATCH), "")
        if if_match and if_match != etag:
            return _status(start_response, "412 Precondition Failed")
        ranged = environ.get("HTTP_RANGE", "")
        if ranged:
            first, _, last = ranged.removeprefix("bytes=").partition("-")
            part = content[int(first) : int(last) + 1 if last else len(content)]
            start_response(
                "206 Partial Content",
                [("Content-Type", "application/octet-stream"), ("Content-Length", str(len(part)))],
            )
            return [part]
        start_response(
            "200 OK",
            [("Content-Type", "application/octet-stream"), ("Content-Length", str(len(content)))],
        )
        return [content]


def _status(start_response, status: str) -> list[bytes]:  # type: ignore[no-untyped-def]
    start_response(status, [("Content-Length", "0")])
    return [b""]


def _unimplemented(start_response) -> list[bytes]:  # type: ignore[no-untyped-def]
    """What a server answers for a procedure it does not serve."""
    body = json.dumps({"code": "unimplemented", "message": "not served by the fake"}).encode()
    start_response(
        "404 Not Found", [("Content-Type", "application/json"), ("Content-Length", str(len(body)))]
    )
    return [body]
