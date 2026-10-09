"""An in-memory Paladin data plane for the tests of a program built on the SDK.

    with paladin.testing.FakePaladin() as fake:
        p = fake.connect()
        obj = paladin.upload(p.data, parent=str(fake.collection()), …)

It serves ObjectService (upload, complete, get, lookup, list, download,
delete), MultipartUploadService, ListParts included, PresignService
(RegenerateUploadUrl, PresignDownload) and StorageBootstrapService, with presigned URLs on its own storage; every other
RPC answers Unimplemented, as a server that lacks it does. Like the server it
runs protovalidate on every request, after authentication, and checks
DeleteObject's ``resource_version``; ListObjects refuses a filter or an
ordering as Unimplemented rather than ignore it. It needs the ``testing``
extra, for protovalidate. Like the server it
binds every upload URL to the size and checksum the upload was registered
with — its storage refuses a PUT without exactly the signed headers, a body of
another length or SHA-256, or an overwrite — records that checksum on the
object, so a download verifies what it reads, and it answers Range and
If-Match requests. ``requests()`` lists the RPCs it
received, with their headers; ``storage_ops()`` the storage requests, and
``fail_storage`` answers them with a fault — an expired URL, a busy store;
``fail_rpc`` makes a procedure's next calls fail with a code as the server
sends it, and ``fail_rpc_if`` only the calls a test picks out by their
message, which ``requests()`` and ``calls()`` carry. With
``strict_auth=True`` it checks credentials as the server's data plane does,
accepting only those it issued that were not revoked, for the tenant the call
names. It runs on the standard library's WSGI server on loopback, each
connection on its own thread.
"""

from __future__ import annotations

import base64
import hashlib
import io
import json
import secrets
import threading
import uuid
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from http import HTTPStatus
from socketserver import ThreadingMixIn
from types import TracebackType
from typing import TYPE_CHECKING, Any
from urllib.parse import parse_qs, urlencode
from wsgiref.simple_server import WSGIRequestHandler, WSGIServer, make_server

from connectrpc.code import Code
from connectrpc.errors import ConnectError
from connectrpc.method import IdempotencyLevel
from connectrpc.request import RequestContext
from google.protobuf import descriptor_pool
from google.protobuf.message import Message
from google.rpc import error_details_pb2

from paladin.client import (
    HEADER_API_TOKEN,
    HEADER_AUTHORIZATION,
    HEADER_CAPABILITY,
    HEADER_IDEMPOTENCY_KEY,
)
from paladin.common.v1 import error_reason_pb2, pagination_pb2, resource_pb2
from paladin.connect import Endpoints, Paladin, connect
from paladin.data.v1 import (
    multipart_service_pb2,
    object_service_pb2,
    presign_service_pb2,
    storage_bootstrap_service_pb2,
    types_pb2,
)
from paladin.data.v1.multipart_service_connect import (
    MultipartUploadServiceSync,
    MultipartUploadServiceWSGIApplication,
)
from paladin.data.v1.object_service_connect import ObjectServiceSync, ObjectServiceWSGIApplication
from paladin.data.v1.presign_service_connect import (
    PresignServiceSync,
    PresignServiceWSGIApplication,
)
from paladin.data.v1.storage_bootstrap_service_connect import (
    StorageBootstrapServiceSync,
    StorageBootstrapServiceWSGIApplication,
)
from paladin.errors import ERROR_DOMAIN, error_details
from paladin.names import API_TOKEN_PREFIX, CollectionName, InvalidNameError, ObjectName
from paladin.transfer import CHECKSUM_SHA256

if TYPE_CHECKING:  # annotations only: typing.Self is 3.11+
    from typing import Self

DEFAULT_COLLECTION = "default"
DEFAULT_PUBLIC_COLLECTION = "public"
"""The collection ``public_collection`` names when given none."""
PUBLIC_CACHE_CONTROL = "public, max-age=31536000, immutable"
"""What the fake stores every public object with: the server's default for a
public collection."""
"""The collection ``collection()`` names."""
PART_SIZE = 5 << 20
"""The part size multipart uploads are told to use."""
DEFAULT_PAGE_SIZE = 100
"""The page ``list_objects`` answers when asked for none."""
TESTING_EXTRA_HINT = "paladin.testing needs the 'testing' extra: pip install 'paladin-sdk[testing]'"
"""What ``FakePaladin`` raises, as an ``ImportError``, without the extra."""
_VALIDATION_FAILED = "validation error"
"""How the server's answer to a request its contract's rules refuse begins."""
EXPIRED_BODY = "<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>"
"""What S3 answers, with 403, for a presigned URL past its expiry."""
DEFAULT_DOWNLOAD_TTL = timedelta(minutes=15)
"""How long a ``presign_download`` URL lives when the request names no TTL:
the server's ``limits.presign.get_ttl``."""
MAX_PRESIGN_TTL = timedelta(hours=168)
"""The longest TTL a request may name: the server's ``limits.presign.max_ttl``.
A longer one is refused, not shortened."""

_LOOPBACK = "127.0.0.1"
_EPHEMERAL_PORT = 0
_STORAGE = "/storage/"
_PUBLIC = "/public/"
"""Where the fake serves public objects, as a store under a public bucket
policy does."""
_PUBLIC_KEY_BYTES = 16
"""The randomness in a public object's key, as the server draws it."""
_CACHE_CONTROL = "Cache-Control"
_PART = "part"
# Carries a download's Content-Disposition, as S3's presigned GET does.
_DISPOSITION_QUERY = "response-content-disposition"
_CONTENT_DISPOSITION = "Content-Disposition"
# expires_at as the server formats it: RFC 3339, UTC, whole seconds.
_RFC3339_UTC = "%Y-%m-%dT%H:%M:%SZ"
_OBJECTS_SEP = "/objects/"
_PUT = "PUT"
_GET = "GET"
_AVAILABLE = types_pb2.OBJECT_STATE_AVAILABLE
_PENDING = types_pb2.OBJECT_STATE_PENDING
_DELETED = types_pb2.OBJECT_STATE_DELETED
_FAILED = types_pb2.OBJECT_STATE_FAILED
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
    cache_control: str = ""

    def headers(self) -> dict[str, str]:
        h = {_CONTENT_LENGTH: str(self.size), _CHECKSUM_SHA256_HEADER: self.checksum}
        if self.content_type:
            h[_CONTENT_TYPE] = self.content_type
        if self.no_overwrite:
            h[_IF_NONE_MATCH] = _IF_NONE_MATCH_ANY
        if self.cache_control:
            h[_CACHE_CONTROL] = self.cache_control
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
    cache_control: str = ""
    """What a public object is stored with; "" elsewhere."""


@dataclass(frozen=True)
class Request:
    """One RPC the fake received."""

    procedure: str
    """The RPC, ``/paladin.data.v1.ObjectService/GetObject``."""
    headers: dict[str, str]
    """What the client sent, by lower-case name: credentials, the idempotency
    key, the User-Agent."""
    message: Message | None = None
    """The request message, e.g. a ``GetObjectRequest``: a copy, so changing
    it changes nothing in the fake."""

    def _copy(self) -> Request:
        return Request(self.procedure, dict(self.headers), _copy_message(self.message))


def _copy_message(message: Message | None) -> Message | None:
    if message is None:
        return None
    out = type(message)()
    out.CopyFrom(message)
    return out


@dataclass(frozen=True)
class StorageOp:
    """One request the fake's storage received."""

    method: str
    path: str
    """The presigned URL's path and query, ``/storage/{id}?part=2``."""


StorageFault = Callable[[StorageOp], "tuple[int, str] | None"]
"""Decides a storage request's answer before the fake does: None lets the fake
answer it, a ``(status, body)`` is answered instead — an expired URL, a busy
store, a refused part."""


@dataclass
class _Multipart:
    name: str
    size: int
    parts: dict[int, bytes] = field(default_factory=dict)
    bindings: dict[int, _Binding] = field(default_factory=dict)


_SERVED_SERVICES = (
    "paladin.data.v1.ObjectService",
    "paladin.data.v1.MultipartUploadService",
    "paladin.data.v1.PresignService",
    "paladin.data.v1.StorageBootstrapService",
)
"""The services the fake mounts; ``fail_rpc`` takes a procedure of any of them,
one the fake answers UNIMPLEMENTED included."""

_SERVER_REASON: dict[Code, int] = {
    Code.NOT_FOUND: error_reason_pb2.ERROR_REASON_NOT_FOUND,
    Code.ABORTED: error_reason_pb2.ERROR_REASON_VERSION_CONFLICT,
    Code.ALREADY_EXISTS: error_reason_pb2.ERROR_REASON_ALREADY_EXISTS,
    Code.INVALID_ARGUMENT: error_reason_pb2.ERROR_REASON_INVALID_ARGUMENT,
    Code.PERMISSION_DENIED: error_reason_pb2.ERROR_REASON_PERMISSION_DENIED,
    Code.FAILED_PRECONDITION: error_reason_pb2.ERROR_REASON_FAILED_PRECONDITION,
    Code.UNAUTHENTICATED: error_reason_pb2.ERROR_REASON_UNAUTHENTICATED,
}
"""The ``ErrorInfo`` reason the server attaches to a code, from the canonical
table in backend/internal/api/apiutil/errmap.go; a code missing here —
UNAVAILABLE, DATA_LOSS, INTERNAL — carries none."""


def _served(procedure: str) -> bool:
    """Whether ``procedure`` is an RPC of a service the fake mounts."""
    service, sep, method = procedure.removeprefix("/").partition("/")
    if not sep or service not in _SERVED_SERVICES:
        return False
    found = descriptor_pool.Default().FindServiceByName(service)
    return method in found.methods_by_name


RequestMatch = Callable[[Message], bool]
"""Picks the calls ``fail_rpc_if`` fails and ``calls`` returns, by their request
message: an object's name, a collection."""


@dataclass(eq=False)
class _RPCFault:
    """One failure ``fail_rpc`` or ``fail_rpc_if`` set: the code, which calls
    it takes — every one when ``match`` is None — and how many more it
    answers."""

    code: Code
    match: RequestMatch | None
    remaining: int


class _Calls:
    """The interceptor every served RPC passes: it records the call, checks
    its credentials under ``strict_auth``, then raises what ``fail_rpc`` set
    for it."""

    def __init__(self, fake: FakePaladin) -> None:
        self._fake = fake

    def intercept_unary_sync(self, call_next, request, ctx: RequestContext):  # type: ignore[no-untyped-def]
        method = ctx.method
        procedure = f"/{method.service_name}/{method.name}"
        headers = dict(ctx.request_headers.items())
        self._fake._record(Request(procedure, headers, _copy_message(request)))
        if self._fake.strict_auth:
            self._fake._authenticate(headers, request)
        # After authentication, as the server's interceptor chain runs it.
        self._fake._validate(request)
        err = self._fake._take_rpc_fault(procedure, request)
        if err is not None:
            raise err
        return self._fake._memoise(procedure, request, ctx, call_next)


# ─── Strict auth ──────────────────────────────────────────────────────────────

_API_TOKEN_PREFIX = API_TOKEN_PREFIX
"""An API token carries the server's prefix, so a client tells it from a
bearer token as it does against the server."""
_BEARER_TOKEN_PREFIX = "paladintest_jwt_"
_CAPABILITY_PREFIX = "paladintest_cap_"
_BEARER_SCHEME = "Bearer "
_CAPABILITY_SCHEME = "capability "
# Fields of a request message that name a resource, whose tenant a credential
# must hold; and the one that names a multipart upload.
_NAMING_FIELDS = ("name", "parent")
_UPLOAD_ID_FIELD = "upload_id"

# The server's answers to a credential it refuses, word for word.
_MISSING_AUTHORIZATION = "missing Authorization header"
_EXPECTED_BEARER = "expected Bearer token"
_EMPTY_TOKEN = "empty token"
_JWT_MALFORMED = "jwt: malformed token"
_JWT_EXPIRED = "jwt: token expired"
_API_TOKEN_NOT_FOUND = "api_token: token not found"
_API_TOKEN_REVOKED = "api_token: token revoked"
_CAPABILITY_INVALID = "capability: invalid signature"
_CAPABILITY_REVOKED = "capability: revoked"
_TENANT_MISMATCH = "URL tenant does not match token tenant"
_NAMES_SPAN_TENANTS = "resource names in one request must name the same tenant"
_UPLOAD_TENANT_MISMATCH = "tenant mismatch"
_KEY_REUSED = (
    "idempotency: this Idempotency-Key was already used for a different request to this method; "
    "use a new key for a new request"
)


@dataclass
class _Credential:
    """One the fake issued: its kind (its prefix), its tenant, and whether it
    was revoked."""

    prefix: str
    tenant: str
    revoked: bool = False


def _api_token(x_header: str, authz: str) -> str:
    """The API token a call carries: the X-header, else a bearer
    Authorization with the API token prefix; "" when it carries none."""
    if x_header.startswith(_API_TOKEN_PREFIX):
        return x_header
    token = authz.removeprefix(_BEARER_SCHEME)
    return token if token != authz and token.startswith(_API_TOKEN_PREFIX) else ""


def _capability_token(x_header: str, authz: str) -> str:
    """The capability a call carries: the X-header, else an Authorization
    with the capability scheme; "" when it carries none."""
    if x_header:
        return x_header
    token = authz.removeprefix(_CAPABILITY_SCHEME)
    return token.strip() if token != authz else ""


def _tenant_of(name: str) -> str | None:
    """The tenant a resource name or a collection names."""
    for parse in (ObjectName.parse, CollectionName.parse):
        try:
            parsed = parse(name)
        except InvalidNameError:
            continue
        collection = parsed.collection if isinstance(parsed, ObjectName) else parsed
        return collection.tenant
    return None


_CONNECTION_BACKLOG = 128
"""Connections the fake queues before it accepts them: room for every test of
a suite sharing one fake to call at once. The standard library's default, 5,
resets the rest."""


class _ThreadingServer(ThreadingMixIn, WSGIServer):
    """Serves each connection on its own thread, so callers sharing the fake
    are not queued behind one another; the fake's state is behind its lock."""

    daemon_threads = True
    request_queue_size = _CONNECTION_BACKLOG


class _Quiet(WSGIRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return None


class FakePaladin(
    ObjectServiceSync, MultipartUploadServiceSync, PresignServiceSync, StorageBootstrapServiceSync
):
    """The fake; a context manager that starts and stops it. Thread-safe."""

    def __init__(self, *, strict_auth: bool = False) -> None:
        """``strict_auth`` checks credentials as the server's data plane does,
        instead of serving every call; see ``issue_bearer_token``."""
        protovalidate = _protovalidate()
        self._validator = protovalidate.Validator()
        self._validation_error: type[Exception] = protovalidate.ValidationError
        self.strict_auth = strict_auth
        """Whether the fake checks credentials."""
        self.tenant = str(uuid.uuid4())
        """The id of the fake's one tenant."""
        self.url = ""
        """The base URL, for every plane; set once started."""
        self._lock = threading.Lock()
        self._objects: dict[str, _Object] = {}
        self._uploads: dict[str, _Multipart] = {}
        self._buckets: set[tuple[str, str]] = set()
        self._bound: set[str] = set()
        self._public: set[str] = set()
        self._requests: list[Request] = []
        self._fault: StorageFault | None = None
        self._after_store: StorageFault | None = None
        self._storage_ops: list[StorageOp] = []
        self._rpc_faults: dict[str, list[_RPCFault]] = {}
        self._credentials: dict[str, _Credential] = {}
        # Responses memoised per (procedure, idempotency key), with the
        # fingerprint of the request each answered — as the server keeps.
        self._replays: dict[tuple[str, str], tuple[bytes, Any]] = {}
        self._httpd: WSGIServer | None = None

    # ─── Lifecycle ──────────────────────────────────────────────────────────

    def __enter__(self) -> Self:
        faults = (_Calls(self),)
        apps = [
            ObjectServiceWSGIApplication(self, interceptors=faults),
            MultipartUploadServiceWSGIApplication(self, interceptors=faults),
            PresignServiceWSGIApplication(self, interceptors=faults),
            StorageBootstrapServiceWSGIApplication(self, interceptors=faults),
        ]

        def route(environ, start_response):  # type: ignore[no-untyped-def]
            if environ["PATH_INFO"].startswith(_STORAGE):
                return self._storage(environ, start_response)
            if environ["PATH_INFO"].startswith(_PUBLIC):
                return self._serve_public(environ, start_response)
            # wsgiref hands over the raw socket, which blocks past the body.
            length = int(environ.get("CONTENT_LENGTH") or 0)
            environ["wsgi.input"] = io.BytesIO(environ["wsgi.input"].read(length))
            for app in apps:
                if environ["PATH_INFO"].startswith(app.path + "/"):
                    return app(environ, start_response)
            return _unimplemented(start_response)

        self._httpd = make_server(
            _LOOPBACK, _EPHEMERAL_PORT, route, server_class=_ThreadingServer, handler_class=_Quiet
        )
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

    def public_collection(self, name: str = DEFAULT_PUBLIC_COLLECTION) -> CollectionName:
        """A public collection in the fake's tenant: the fake names the objects
        uploaded into it and serves each, unsigned, at its ``public_url``."""
        c = self.collection(name)
        with self._lock:
            self._public.add(str(c))
        return c

    # ─── Inspection ─────────────────────────────────────────────────────────

    def put(
        self, collection: CollectionName, key: str, content_type: str, body: bytes
    ) -> types_pb2.Object:
        """Store an object directly, as if uploaded and completed."""
        with self._lock:
            key = self._public_key(str(collection), key)
            o = self._new(str(collection), key, content_type)
            self._commit(o, body, "")
            return o.msg

    def requests(self) -> list[Request]:
        """The RPCs received, oldest first, each with a copy of its message;
        one the fake does not serve is not among them."""
        with self._lock:
            return [r._copy() for r in self._requests]

    def calls(self, procedure: str, match: RequestMatch | None = None) -> list[Request]:
        """The requests for ``procedure`` whose message ``match`` accepts,
        oldest first; None accepts every one. A test that shares the fake with
        others counts its own calls by what they named::

            fake.calls(GET_OBJECT, lambda m: m.name == obj.name)

        ``match`` gets a copy of each message, and runs without the fake's
        lock held."""
        return [
            r
            for r in self.requests()
            if r.procedure == procedure
            and (match is None or (r.message is not None and match(r.message)))
        ]

    def fail_storage(self, fault: StorageFault | None) -> None:
        """Answer storage requests through ``fault`` before the fake does;
        None stops it."""
        with self._lock:
            self._fault = fault

    def fail_storage_after_storing(self, fault: StorageFault | None) -> None:
        """Like ``fail_storage``, but the fake first stores what a PUT carried:
        the answer is lost, not the bytes — the case a retried PUT meets as
        412."""
        with self._lock:
            self._after_store = fault

    def fail_rpc(self, procedure: str, times: int, code: Code) -> Callable[[], None]:
        """Make the next ``times`` calls of ``procedure`` — e.g.
        ``/paladin.data.v1.ObjectService/GetObject`` — answer ``code``, as the
        server answers it: with the ``ErrorInfo`` reason the server attaches to
        that code, so the SDK's typed errors and ``reason`` read it. Calls after
        those are served as usual, and every failed one is in ``requests()``.
        A later ``fail_rpc`` on the same procedure replaces this one; the
        returned function clears this one's remaining failures, and nothing
        else.

        Raises ValueError for a procedure the fake does not serve or a
        ``times`` below 1, and TypeError for a ``code`` that is not a ``Code``:
        each is a mistake in the test, not a scenario."""
        return self._fail_rpc("fail_rpc", procedure, None, times, code)

    def fail_rpc_if(
        self, procedure: str, match: RequestMatch, times: int, code: Code
    ) -> Callable[[], None]:
        """``fail_rpc`` for the calls of ``procedure`` whose request ``match``
        accepts — an object's name, a collection — so tests sharing one fake
        fail only their own calls; every other call of the procedure is
        served. Its failures stack: each adds one, and a call is taken by the
        most recent one that matches it. ``match`` gets a copy of the request,
        runs without the fake's lock held, and must not block.

        Raises as ``fail_rpc`` does, and TypeError for a ``match`` that is not
        callable."""
        if not callable(match):
            raise TypeError("fail_rpc_if: match is not callable; fail_rpc fails every call")
        return self._fail_rpc("fail_rpc_if", procedure, match, times, code)

    def _fail_rpc(
        self,
        caller: str,
        procedure: str,
        match: RequestMatch | None,
        times: int,
        code: Code,
    ) -> Callable[[], None]:
        if not _served(procedure):
            raise ValueError(f"{caller}: {procedure!r} is not a procedure the fake serves")
        if times < 1:
            raise ValueError(f"{caller}: times is {times}, want at least 1")
        if not isinstance(code, Code):
            raise TypeError(f"{caller}: {code!r} is not a connectrpc Code")
        fault = _RPCFault(code, match, times)
        with self._lock:
            faults = self._rpc_faults.get(procedure, [])
            if match is None:
                # A later fail_rpc replaces the earlier one, as it always has.
                faults = [f for f in faults if f.match is not None]
            self._rpc_faults[procedure] = [*faults, fault]

        def reset() -> None:
            with self._lock:
                self._drop_rpc_fault(procedure, fault)

        return reset

    def _drop_rpc_fault(self, procedure: str, fault: _RPCFault) -> None:
        """Remove ``fault`` from ``procedure``'s failures; the caller holds the
        lock."""
        left = [f for f in self._rpc_faults.get(procedure, []) if f is not fault]
        if left:
            self._rpc_faults[procedure] = left
        else:
            self._rpc_faults.pop(procedure, None)

    def _take_rpc_fault(self, procedure: str, message: Message) -> ConnectError | None:
        """The error ``fail_rpc`` or ``fail_rpc_if`` set for this call of
        ``procedure``, None when none takes it, counting the call against the
        one that does. A match runs without the lock."""
        with self._lock:
            faults = list(self._rpc_faults.get(procedure, []))
        for fault in reversed(faults):
            if fault.match is not None and not fault.match(_copy_message(message)):  # type: ignore[arg-type]
                continue
            with self._lock:
                live = any(f is fault for f in self._rpc_faults.get(procedure, []))
                if live:
                    fault.remaining -= 1
                    if fault.remaining == 0:
                        self._drop_rpc_fault(procedure, fault)
            if live:  # another call may have spent it meanwhile
                return _failure(procedure, fault.code)
        return None

    # ─── Credentials ────────────────────────────────────────────────────────

    def issue_bearer_token(self, tenant: str) -> str:
        """A bearer token for ``tenant`` — ``tenant`` attribute for the fake's
        own — as an OIDC JWT is, for ``connect(bearer_token=…)``.

        With ``strict_auth`` a call must carry a credential the fake issued,
        not revoked, and every resource it names must be in that credential's
        tenant. The answers are the server's, codes and messages, with no
        ``ErrorInfo`` reason, as the server's authentication sends none: no
        credential, or a bearer or API token the fake did not issue or
        revoked, is UNAUTHENTICATED; a capability it did not issue or revoked
        is PERMISSION_DENIED, as the server answers every capability it cannot
        verify; a name or parent in another tenant, or another tenant's
        multipart upload, is PERMISSION_DENIED. The fake checks only that: no
        signature, Biscuit, caveat, scope, audience or expiry, and no platform
        admin acting in another tenant. Credentials are checked before
        ``fail_rpc``'s failures."""
        return self._issue(_BEARER_TOKEN_PREFIX, tenant)

    def issue_api_token(self, tenant: str) -> str:
        """An API token for ``tenant``, for ``connect(api_token=…)`` or
        ``connect(bearer_token=…)``."""
        return self._issue(_API_TOKEN_PREFIX, tenant)

    def issue_capability(self, tenant: str) -> str:
        """A capability for ``tenant``, for ``connect(capability=…)``. It is
        the fake's own token, not a Biscuit: the fake checks only that it
        issued it, did not revoke it, and that it names the tenant."""
        return self._issue(_CAPABILITY_PREFIX, tenant)

    def _issue(self, prefix: str, tenant: str) -> str:
        token = prefix + uuid.uuid4().hex
        with self._lock:
            self._credentials[token] = _Credential(prefix, tenant)
        return token

    def revoke(self, token: str) -> None:
        """Revoke a credential the fake issued: with ``strict_auth``, a call
        that carries it is refused from then on — UNAUTHENTICATED for a bearer
        or API token, PERMISSION_DENIED for a capability, as the server
        answers. A token the fake did not issue is ignored."""
        with self._lock:
            credential = self._credentials.get(token)
            if credential is not None:
                credential.revoked = True

    def _authenticate(self, headers: Mapping[str, str], message: Message) -> None:
        """Raise the server's answer to a call's credentials; return when it
        lets the call through."""
        tenant = self._principal_tenant(headers)
        self._authorize_tenant(tenant, message)

    def _principal_tenant(self, headers: Mapping[str, str]) -> str:
        """The tenant of the call's credential, found as the server's
        interceptors find it: an API token first, then a capability, then a
        bearer token."""
        authz = headers.get(HEADER_AUTHORIZATION.lower(), "")
        token = _api_token(headers.get(HEADER_API_TOKEN.lower(), ""), authz)
        if token:
            return self._verify(
                token,
                _API_TOKEN_PREFIX,
                Code.UNAUTHENTICATED,
                _API_TOKEN_NOT_FOUND,
                _API_TOKEN_REVOKED,
            )
        token = _capability_token(headers.get(HEADER_CAPABILITY.lower(), ""), authz)
        if token:
            return self._verify(
                token,
                _CAPABILITY_PREFIX,
                Code.PERMISSION_DENIED,
                _CAPABILITY_INVALID,
                _CAPABILITY_REVOKED,
            )
        if not authz:
            raise ConnectError(Code.UNAUTHENTICATED, _MISSING_AUTHORIZATION)
        if not authz.startswith(_BEARER_SCHEME):
            raise ConnectError(Code.UNAUTHENTICATED, _EXPECTED_BEARER)
        token = authz.removeprefix(_BEARER_SCHEME).strip()
        if not token:
            raise ConnectError(Code.UNAUTHENTICATED, _EMPTY_TOKEN)
        return self._verify(
            token, _BEARER_TOKEN_PREFIX, Code.UNAUTHENTICATED, _JWT_MALFORMED, _JWT_EXPIRED
        )

    def _verify(self, token: str, prefix: str, code: Code, unknown: str, revoked: str) -> str:
        """The tenant of ``token``, a credential issued with ``prefix``; raise
        ``code`` with ``unknown`` or ``revoked`` otherwise."""
        with self._lock:
            credential = self._credentials.get(token)
        if credential is None or credential.prefix != prefix:
            raise ConnectError(code, unknown)
        if credential.revoked:
            raise ConnectError(code, revoked)
        return credential.tenant

    def _authorize_tenant(self, tenant: str, message: Message) -> None:
        """Refuse a call that names a resource outside ``tenant``: its name or
        parent, or the object of the multipart upload it names."""
        fields = message.DESCRIPTOR.fields_by_name
        named: str | None = None
        for field_name in _NAMING_FIELDS:
            if field_name not in fields:
                continue
            value = getattr(message, field_name)
            found = _tenant_of(value) if isinstance(value, str) else None
            if found is None:
                continue  # not a resource name: the RPC refuses it itself
            if named is not None and found != named:
                raise ConnectError(Code.PERMISSION_DENIED, _NAMES_SPAN_TENANTS)
            named = found
        if named is not None and named != tenant:
            raise ConnectError(Code.PERMISSION_DENIED, _TENANT_MISMATCH)
        if _UPLOAD_ID_FIELD in fields:
            with self._lock:
                up = self._uploads.get(getattr(message, _UPLOAD_ID_FIELD))
                o = self._objects.get(up.name) if up is not None else None
            if o is not None and o.msg.tenant_id != tenant:
                raise ConnectError(Code.PERMISSION_DENIED, _UPLOAD_TENANT_MISMATCH)

    def mark_failed(self, name: str) -> None:
        """Fail a PENDING object, as the server's reconciler does when its
        upload URL expired with no bytes stored."""
        with self._lock:
            o = self._objects.get(name)
            if o is not None and o.msg.state == _PENDING:
                o.msg.state = _FAILED
                _bump(o)

    def storage_ops(self) -> list[StorageOp]:
        """The storage requests received, oldest first."""
        with self._lock:
            return list(self._storage_ops)

    def _memoise(self, procedure: str, request: Message, ctx: RequestContext, call_next):  # type: ignore[no-untyped-def]
        """Answer as the server's idempotency interceptor does: a call
        declared free of side effects runs as it is; one with a key replays
        the response first given to the same request with that key, and is
        refused when the key last went with a different request. A test that
        shares one key across requests fails here as against Paladin."""
        if ctx.method.idempotency_level == IdempotencyLevel.NO_SIDE_EFFECTS:
            return call_next(request, ctx)
        key = ctx.request_headers.get(HEADER_IDEMPOTENCY_KEY.lower(), "")
        if not key:
            return call_next(request, ctx)
        fingerprint = hashlib.sha256(request.SerializeToString(deterministic=True)).digest()
        slot = (procedure, key)
        with self._lock:
            prior = self._replays.get(slot)
        if prior is not None:
            if prior[0] != fingerprint:
                raise ConnectError(Code.INVALID_ARGUMENT, _KEY_REUSED)
            return prior[1]
        response = call_next(request, ctx)
        with self._lock:
            self._replays[slot] = (fingerprint, response)
        return response

    def _record(self, request: Request) -> None:
        with self._lock:
            self._requests.append(request)

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
            resource_version="1",
        )
        o = _Object(msg)
        if parent in self._public:
            msg.public_url = f"{self.url}{_PUBLIC}{self.tenant}/{msg.collection}/{msg.key}"
            o.cache_control = PUBLIC_CACHE_CONTROL
        self._objects[msg.name] = o
        return o

    def _public_key(self, parent: str, key: str) -> str:
        """The key a public collection names an object with, refusing one the
        client chose, as the server does; ``key`` elsewhere."""
        if parent not in self._public:
            return key
        if key:
            raise _public_rule("a public collection names its objects itself; leave the key empty")
        return base64.b32encode(secrets.token_bytes(_PUBLIC_KEY_BYTES)).decode().rstrip("=").lower()

    def _claim(self, request: Any) -> _Object:
        """The object an upload registers, refusing a key another object
        holds — in any state, the trash included — as the server's unique path
        does."""
        key = self._public_key(request.parent, request.key)
        if key:
            prefix = request.parent + _OBJECTS_SEP
            for o in self._objects.values():
                if o.msg.name.startswith(prefix) and o.msg.key == key:
                    raise ConnectError(Code.ALREADY_EXISTS, f"an object is already at {key!r}")
        o = self._new(request.parent, key, request.content_type)
        o.msg.metadata.update(request.metadata)
        o.msg.tags.update(request.tags)
        return o

    @staticmethod
    def _commit(o: _Object, body: bytes, checksum: str) -> None:
        o.body = body
        o.msg.etag = _etag(body)
        o.msg.size_bytes = len(body)
        o.msg.state = _AVAILABLE
        _bump(o)
        if checksum:
            o.msg.checksum.algorithm = CHECKSUM_SHA256
            o.msg.checksum.value = checksum

    def _signed(self, path: str, method: str) -> resource_pb2.PresignedUrl:
        return resource_pb2.PresignedUrl(url=f"{self.url}{_STORAGE}{path}", method=method)

    def _validate(self, request: Message) -> None:
        """Refuse a request the contract's rules refuse, as the server's
        validate interceptor does: protovalidate's message, and the
        violations as a buf.validate.Violations detail."""
        try:
            self._validator.validate(request)
        except self._validation_error as err:
            # Each violation named by its field, as the server's message does.
            named = "; ".join(
                ".".join(e.field_name for e in v.proto.field.elements) + f": {v.proto.message}"
                for v in err.violations  # type: ignore[attr-defined]
            )
            raise ConnectError(
                Code.INVALID_ARGUMENT,
                f"{_VALIDATION_FAILED}: {named}",
                # protovalidate builds its Violations with connectrpc's own
                # Protobuf runtime, which a ConnectError detail takes as is.
                [err.to_proto()],  # type: ignore[attr-defined]
            ) from err

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
            o = self._claim(request)
            o.bound = _Binding(
                request.size_hint_bytes,
                request.checksum_value,
                request.content_type,
                True,
                o.cache_control,
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
                    and o.msg.state != _DELETED
                ):
                    return o.msg
        raise ConnectError(Code.NOT_FOUND, f"{request.parent} has no key {request.key!r}")

    def list_objects(self, request, ctx):  # type: ignore[no-untyped-def]
        unhonoured = _unhonoured_list_field(request)
        if unhonoured:
            raise ConnectError(
                Code.UNIMPLEMENTED,
                f"paladin.testing: ListObjects does not apply {unhonoured}; the server does, "
                "so a test would pass on what it would not",
            )
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
            if request.permanent:
                # As the server: one in the trash is purged too, and the path
                # is free again.
                o = self._objects.get(request.name)
                if o is None:
                    raise ConnectError(Code.NOT_FOUND, f"{request.name} not found")
                _check_version(o, request.resource_version)
                del self._objects[request.name]
                return object_service_pb2.DeleteObjectResponse()
            o = self._get(request.name)
            _check_version(o, request.resource_version)
            if o.msg.public_url:
                raise _public_rule(
                    "a public object is deleted with permanent=true; it has no trash"
                )
            o.msg.state = _DELETED  # in the trash, still holding its key
            _bump(o)
            o.body = None
            return object_service_pb2.DeleteObjectResponse()

    # ─── PresignService ─────────────────────────────────────────────────────

    def regenerate_upload_url(self, request, ctx):  # type: ignore[no-untyped-def]
        """Presigns a PENDING object's upload URL again, bound as the first
        was."""
        with self._lock:
            o = self._get(request.name)
            if o.msg.state != _PENDING or o.bound is None:
                raise ConnectError(Code.FAILED_PRECONDITION, "the object is not pending")
            url = self._signed(o.msg.object_id, _PUT)
            url.required_headers.update(o.bound.headers())
            return presign_service_pb2.RegenerateUploadUrlResponse(upload_url=url)

    def presign_download(self, request, ctx):  # type: ignore[no-untyped-def]
        """Presigns a GET of an AVAILABLE object on the fake's storage, as the
        server does: an absent TTL is ``DEFAULT_DOWNLOAD_TTL``, one that is
        negative or above ``MAX_PRESIGN_TTL`` is INVALID_ARGUMENT, a name that
        is not an object's INVALID_ARGUMENT, an unknown object NOT_FOUND, and
        one that is not AVAILABLE FAILED_PRECONDITION."""
        try:
            ObjectName.parse(request.name)
        except InvalidNameError as err:
            raise ConnectError(Code.INVALID_ARGUMENT, str(err)) from err
        ttl = request.ttl.ToTimedelta() if request.HasField("ttl") else timedelta(0)
        if ttl < timedelta(0) or ttl > MAX_PRESIGN_TTL:
            raise ConnectError(Code.INVALID_ARGUMENT, f"ttl {ttl} is outside 0..{MAX_PRESIGN_TTL}")
        ttl = ttl or DEFAULT_DOWNLOAD_TTL
        with self._lock:
            o = self._get(request.name)
            if o.msg.state != _AVAILABLE:
                state = types_pb2.ObjectState.Name(o.msg.state)
                raise ConnectError(
                    Code.FAILED_PRECONDITION, f"object state {state} does not allow GET"
                )
            path = o.msg.object_id
            if request.content_disposition:
                path += "?" + urlencode({_DISPOSITION_QUERY: request.content_disposition})
            url = self._signed(path, _GET)
            url.expires_at_rfc3339 = (datetime.now(UTC) + ttl).strftime(_RFC3339_UTC)
            if request.require_etag_match:
                url.required_headers[_IF_MATCH] = f'"{o.msg.etag}"'
            return presign_service_pb2.PresignDownloadResponse(download_url=url)

    # ─── MultipartUploadService ─────────────────────────────────────────────

    def initiate_multipart_upload(self, request, ctx):  # type: ignore[no-untyped-def]
        self._parent(request.parent)
        if request.size_bytes <= 0:
            raise ConnectError(Code.INVALID_ARGUMENT, "size_bytes must be positive")
        with self._lock:
            o = self._claim(request)
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
            # As the server: the composite of the parts' checksums, over the
            # part size the upload was told to use.
            o.msg.checksum.algorithm = CHECKSUM_SHA256
            o.msg.checksum.value = _composite_sha256([p.checksum_value for p in request.parts])
            o.msg.checksum.part_size_bytes = PART_SIZE
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

    def _serve_public(self, environ, start_response):  # type: ignore[no-untyped-def]
        """An unsigned GET of a public object, as the store answers it: the
        bytes with the collection's Cache-Control, 404 once it is gone."""
        if environ["REQUEST_METHOD"] != _GET:
            return _status(start_response, HTTPStatus.FORBIDDEN, "anonymous access is read-only")
        url = self.url + environ["PATH_INFO"]
        with self._lock:
            found = next((o for o in self._objects.values() if o.msg.public_url == url), None)
            # The store knows nothing of the object's state: it serves what a
            # PUT stored before CompleteObject as readily as the committed body.
            body = None
            if found is not None and found.msg.state == _AVAILABLE:
                body = found.body
            elif found is not None and found.msg.state == _PENDING:
                body = found.put
            content_type = found.msg.content_type if found is not None else ""
        if body is None:
            return _status(start_response, HTTPStatus.NOT_FOUND, "no such key")
        start_response(
            "200 OK",
            [
                (_CONTENT_TYPE, content_type),
                (_CACHE_CONTROL, PUBLIC_CACHE_CONTROL),
                (_CONTENT_LENGTH, str(len(body))),
            ],
        )
        return [body]

    def _storage(self, environ, start_response):  # type: ignore[no-untyped-def]
        object_id = environ["PATH_INFO"][len(_STORAGE) :]
        raw_query = environ.get("QUERY_STRING", "")
        query = parse_qs(raw_query)
        method = environ["REQUEST_METHOD"]
        op = StorageOp(method, environ["PATH_INFO"] + (f"?{raw_query}" if raw_query else ""))
        body = (
            environ["wsgi.input"].read(int(environ.get("CONTENT_LENGTH") or 0))
            if method == _PUT
            else b""
        )
        with self._lock:
            self._storage_ops.append(op)
            fault, after_store = self._fault, self._after_store
        failed = fault(op) if fault is not None else None
        if failed is not None:
            return _status(start_response, *failed)
        if method == _PUT:
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
            lost = after_store(op) if after_store is not None else None
            if lost is not None:
                return _status(start_response, *lost)
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
        headers = [("Content-Type", "application/octet-stream")]
        if _DISPOSITION_QUERY in query:
            headers.append((_CONTENT_DISPOSITION, query[_DISPOSITION_QUERY][0]))
        if_match = environ.get(_environ_key(_IF_MATCH), "")
        if if_match and if_match != etag:
            return _status(start_response, "412 Precondition Failed")
        ranged = environ.get("HTTP_RANGE", "")
        if ranged:
            first, _, last = ranged.removeprefix("bytes=").partition("-")
            part = content[int(first) : int(last) + 1 if last else len(content)]
            start_response("206 Partial Content", [*headers, ("Content-Length", str(len(part)))])
            return [part]
        start_response("200 OK", [*headers, ("Content-Length", str(len(content)))])
        return [content]


def _public_rule(what: str) -> ConnectError:
    """The server's answer to a request a public collection refuses."""
    return ConnectError(
        Code.FAILED_PRECONDITION,
        f"public collection: {what}",
        error_details(
            [
                error_details_pb2.ErrorInfo(
                    reason=error_reason_pb2.ErrorReason.Name(
                        error_reason_pb2.ERROR_REASON_PUBLIC_COLLECTION_RULE
                    ),
                    domain=ERROR_DOMAIN,
                )
            ]
        ),
    )


def _failure(procedure: str, code: Code) -> ConnectError:
    """What an injected failure answers with: ``code``, and the reason the
    server attaches to it."""
    return _with_reason(code, f"{procedure} failed by fail_rpc")


def _with_reason(code: Code, message: str) -> ConnectError:
    """``code`` with the ``ErrorInfo`` reason the server attaches to it."""
    reason = _SERVER_REASON.get(code)
    details = (
        [
            error_details_pb2.ErrorInfo(
                reason=error_reason_pb2.ErrorReason.Name(reason), domain=ERROR_DOMAIN
            )
        ]
        if reason is not None
        else []
    )
    return ConnectError(code, message, error_details(details))


def _composite_sha256(parts: list[str]) -> str:
    """The server's composite of SHA-256 part checksums: the base64 SHA-256 of
    their raw digests, in part order, then "-" and the count."""
    whole = hashlib.sha256(b"".join(base64.b64decode(p) for p in parts)).digest()
    return f"{base64.b64encode(whole).decode()}-{len(parts)}"


def _bump(o: _Object) -> None:
    """Advance the object's resource_version, as the server's trigger does on
    every change to its row."""
    o.msg.resource_version = str(int(o.msg.resource_version) + 1)


def _check_version(o: _Object, version: str) -> None:
    """Refuse a resource_version that is not the object's, as the server
    does: not a number is INVALID_ARGUMENT, another one ABORTED."""
    try:
        int(version)
    except ValueError as err:
        raise _with_reason(Code.INVALID_ARGUMENT, f"invalid resource_version: {err}") from err
    if version != o.msg.resource_version:
        raise _with_reason(Code.ABORTED, "resource_version mismatch")


def _unhonoured_list_field(request: object_service_pb2.ListObjectsRequest) -> str:
    """The ListObjects parameter the fake does not apply, or "" for none."""
    if request.filter:
        return "filter"
    if request.order_by:
        return "order_by"
    if request.sort_order != pagination_pb2.SORT_ORDER_UNSPECIFIED:
        return "sort_order"
    return ""


def _protovalidate() -> Any:
    """The protovalidate module, from the ``testing`` extra."""
    try:
        import protovalidate
    except ImportError as err:
        raise ImportError(TESTING_EXTRA_HINT) from err
    return protovalidate


def _status(start_response, status: str | int, body: str = "") -> list[bytes]:  # type: ignore[no-untyped-def]
    if isinstance(status, int):
        status = f"{status} {HTTPStatus(status).phrase}"
    encoded = body.encode()
    start_response(status, [("Content-Length", str(len(encoded)))])
    return [encoded]


def _unimplemented(start_response) -> list[bytes]:  # type: ignore[no-untyped-def]
    """What a server answers for a procedure it does not serve."""
    body = json.dumps({"code": "unimplemented", "message": "not served by the fake"}).encode()
    start_response(
        "404 Not Found", [("Content-Type", "application/json"), ("Content-Length", str(len(body)))]
    )
    return [body]
