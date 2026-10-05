"""paladin.testing's request journal, per-call failures and strict auth —
the same scenarios the Go fake's tests run."""

from __future__ import annotations

import uuid
from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf.message import Message

import paladin
from paladin import NotFoundError, ObjectURI, upload
from paladin.common.v1 import error_reason_pb2
from paladin.data.v1 import multipart_service_pb2, object_service_pb2, types_pb2
from paladin.testing import PART_SIZE, FakePaladin

GET_OBJECT = "/paladin.data.v1.ObjectService/GetObject"
LOOKUP_OBJECT = "/paladin.data.v1.ObjectService/LookupObject"
EMPTY_SHA256 = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
BASIC_CREDENTIAL = "Basic dXNlcjpwYXNz"


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


@pytest.fixture
def strict():  # type: ignore[no-untyped-def]
    with FakePaladin(strict_auth=True) as f:
        yield f


def _get(p: paladin.Paladin, obj: types_pb2.Object) -> None:
    p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))


def _named(name: str) -> Callable[[Message], bool]:
    return lambda m: m.name == name  # type: ignore[attr-defined]


# ─── requests() and calls() ─────────────────────────────────────────────────


def test_requests_carry_the_message(fake: FakePaladin) -> None:
    p = fake.connect()
    mine = fake.put(fake.collection(), "mine", "text/plain", b"x")
    theirs = fake.put(fake.collection(), "theirs", "text/plain", b"y")
    for obj in (mine, theirs, mine):
        _get(p, obj)
    reqs = fake.requests()
    assert len(reqs) == 3
    assert isinstance(reqs[1].message, object_service_pb2.GetObjectRequest)
    assert reqs[1].message.name == theirs.name
    assert len(fake.calls(GET_OBJECT, _named(mine.name))) == 2
    assert len(fake.calls(GET_OBJECT)) == 3
    assert fake.calls(LOOKUP_OBJECT) == []


def test_requests_are_copies(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    _get(p, obj)
    first = fake.requests()[0]
    first.message.name = "changed"  # type: ignore[union-attr]
    first.headers["user-agent"] = "changed"
    again = fake.requests()[0]
    assert again.message.name == obj.name  # type: ignore[union-attr]
    assert again.headers["user-agent"] != "changed"


# ─── fail_rpc_if ────────────────────────────────────────────────────────────


def test_fail_rpc_if_fails_only_matching_calls(fake: FakePaladin) -> None:
    times = 2
    p = fake.connect()
    mine = fake.put(fake.collection(), "mine", "text/plain", b"x")
    theirs = fake.put(fake.collection(), "theirs", "text/plain", b"y")
    fake.fail_rpc_if(GET_OBJECT, _named(mine.name), times, Code.NOT_FOUND)
    for _ in range(times):
        _get(p, theirs)
        with pytest.raises(NotFoundError) as caught:
            _get(p, mine)
        assert paladin.reason(caught.value) == error_reason_pb2.ERROR_REASON_NOT_FOUND
    _get(p, mine)
    assert len(fake.calls(GET_OBJECT, _named(mine.name))) == times + 1


def test_fail_rpc_if_isolates_parallel_callers(fake: FakePaladin) -> None:
    """Callers sharing one fake each fail their own object, and none takes
    another's failure."""
    callers, times = 8, 3
    p = fake.connect()

    def caller(i: int) -> None:
        obj = fake.put(fake.collection(), f"k{i}", "text/plain", b"x")
        fake.fail_rpc_if(GET_OBJECT, _named(obj.name), times, Code.UNAVAILABLE)
        for _ in range(times):
            with pytest.raises(ConnectError) as caught:
                _get(p, obj)
            assert caught.value.code == Code.UNAVAILABLE
        _get(p, obj)

    with ThreadPoolExecutor(callers) as pool:
        for done in [pool.submit(caller, i) for i in range(callers)]:
            done.result()


def test_fail_rpc_if_stacks(fake: FakePaladin) -> None:
    p = fake.connect()
    a = fake.put(fake.collection(), "a", "text/plain", b"a")
    b = fake.put(fake.collection(), "b", "text/plain", b"b")
    fake.fail_rpc(GET_OBJECT, 1, Code.UNAVAILABLE)
    fake.fail_rpc_if(GET_OBJECT, _named(a.name), 1, Code.NOT_FOUND)
    fake.fail_rpc_if(GET_OBJECT, _named(b.name), 1, Code.PERMISSION_DENIED)
    for obj, code in (
        (b, Code.PERMISSION_DENIED),
        (a, Code.NOT_FOUND),
        (a, Code.UNAVAILABLE),
    ):
        with pytest.raises(ConnectError) as caught:
            _get(p, obj)
        assert caught.value.code == code
    _get(p, a)


def test_fail_rpc_if_reset(fake: FakePaladin) -> None:
    p = fake.connect()
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    reset = fake.fail_rpc_if(GET_OBJECT, _named(obj.name), 5, Code.UNAVAILABLE)
    with pytest.raises(ConnectError):
        _get(p, obj)
    reset()
    _get(p, obj)


@pytest.mark.parametrize(
    ("procedure", "match", "times", "code", "error"),
    [
        (GET_OBJECT, None, 1, Code.UNAVAILABLE, TypeError),
        ("/paladin.admin.v1.TenantService/ListTenants", bool, 1, Code.UNAVAILABLE, ValueError),
        (GET_OBJECT, bool, 0, Code.UNAVAILABLE, ValueError),
        (GET_OBJECT, bool, 1, "unavailable", TypeError),
    ],
    ids=["no match", "a service not served", "zero times", "not a Code"],
)
def test_fail_rpc_if_refuses_a_mistake(
    fake: FakePaladin, procedure: str, match: object, times: int, code: object, error: type
) -> None:
    with pytest.raises(error):
        fake.fail_rpc_if(procedure, match, times, code)  # type: ignore[arg-type]


# ─── strict_auth ────────────────────────────────────────────────────────────

MISSING_AUTHORIZATION = (Code.UNAUTHENTICATED, "missing Authorization header")
EXPECTED_BEARER = (Code.UNAUTHENTICATED, "expected Bearer token")
JWT_MALFORMED = (Code.UNAUTHENTICATED, "jwt: malformed token")
JWT_EXPIRED = (Code.UNAUTHENTICATED, "jwt: token expired")
API_TOKEN_NOT_FOUND = (Code.UNAUTHENTICATED, "api_token: token not found")
API_TOKEN_REVOKED = (Code.UNAUTHENTICATED, "api_token: token revoked")
CAPABILITY_INVALID = (Code.PERMISSION_DENIED, "capability: invalid signature")
CAPABILITY_REVOKED = (Code.PERMISSION_DENIED, "capability: revoked")
TENANT_MISMATCH = (Code.PERMISSION_DENIED, "URL tenant does not match token tenant")
UPLOAD_TENANT_MISMATCH = (Code.PERMISSION_DENIED, "tenant mismatch")


def _refused(call: Callable[[], object], want: tuple[Code, str]) -> None:
    """The call is refused with the server's code and message, and no
    ErrorInfo reason — the server's authentication sends none."""
    with pytest.raises(ConnectError) as caught:
        call()
    assert (caught.value.code, caught.value.message) == want
    assert paladin.reason(caught.value) == error_reason_pb2.ERROR_REASON_UNSPECIFIED


def test_default_mode_serves_every_call(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "k", "text/plain", b"x")
    revoked = fake.issue_bearer_token(fake.tenant)
    fake.revoke(revoked)
    for options in (
        {},
        {"bearer_token": "not-issued"},
        {"bearer_token": fake.issue_bearer_token(str(uuid.uuid4()))},
        {"bearer_token": revoked},
        {"headers": {"Authorization": BASIC_CREDENTIAL}},
        {"capability": fake.issue_capability(str(uuid.uuid4()))},
    ):
        _get(fake.connect(**options), obj)


def test_strict_auth_accepts_an_issued_credential(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    api_token = strict.issue_api_token(strict.tenant)
    capability = strict.issue_capability(strict.tenant)
    for options in (
        {"bearer_token": strict.issue_bearer_token(strict.tenant)},
        {"api_token": api_token},
        {"bearer_token": api_token},
        {"capability": capability},
        {"headers": {"Authorization": f"capability {capability}"}},
        # The server's JWT gate steps aside for a capability, as the fake does.
        {"capability": capability, "bearer_token": "not-issued"},
    ):
        _get(strict.connect(**options), obj)


def test_strict_auth_serves_the_workflows(strict: FakePaladin) -> None:
    p = strict.connect(bearer_token=strict.issue_bearer_token(strict.tenant))
    body = b"p" * (2 * PART_SIZE + 1)
    obj = upload(
        p.data,
        parent=str(strict.collection()),
        key="k",
        content_type="application/octet-stream",
        body=body,
        size=len(body),
        multipart_threshold=PART_SIZE,
    )
    assert strict.content(obj.name) == body


def test_strict_auth_refuses_what_the_server_refuses(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    other = str(uuid.uuid4())
    cases = [
        ({}, MISSING_AUTHORIZATION),
        ({"headers": {"Authorization": BASIC_CREDENTIAL}}, EXPECTED_BEARER),
        ({"bearer_token": "not-issued"}, JWT_MALFORMED),
        ({"api_token": "paladin_pat_notissued"}, API_TOKEN_NOT_FOUND),
        ({"capability": "not-issued"}, CAPABILITY_INVALID),
        ({"bearer_token": strict.issue_capability(strict.tenant)}, JWT_MALFORMED),
        ({"bearer_token": strict.issue_bearer_token(other)}, TENANT_MISMATCH),
        ({"api_token": strict.issue_api_token(other)}, TENANT_MISMATCH),
        ({"capability": strict.issue_capability(other)}, TENANT_MISMATCH),
    ]
    for options, want in cases:
        before = len(strict.calls(GET_OBJECT))
        _refused(lambda o=options: _get(strict.connect(**o), obj), want)
        assert len(strict.calls(GET_OBJECT)) == before + 1, options


@pytest.mark.parametrize(
    ("issue", "option", "want"),
    [
        (FakePaladin.issue_bearer_token, "bearer_token", JWT_EXPIRED),
        (FakePaladin.issue_api_token, "api_token", API_TOKEN_REVOKED),
        (FakePaladin.issue_capability, "capability", CAPABILITY_REVOKED),
    ],
    ids=["a bearer token", "an API token", "a capability"],
)
def test_revoke_refuses_the_credential(
    strict: FakePaladin,
    issue: Callable[[FakePaladin, str], str],
    option: str,
    want: tuple[Code, str],
) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    token = issue(strict, strict.tenant)
    p = strict.connect(**{option: token})
    _get(p, obj)
    strict.revoke(token)
    _refused(lambda: _get(p, obj), want)


def test_revoke_then_mint_again(strict: FakePaladin) -> None:
    """A client that mints a fresh capability once the server refuses its own
    gets through again: the path revoke exists to test."""
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    first = strict.issue_capability(strict.tenant)
    strict.revoke(first)
    _refused(lambda: _get(strict.connect(capability=first), obj), CAPABILITY_REVOKED)
    _get(strict.connect(capability=strict.issue_capability(strict.tenant)), obj)


def test_strict_auth_refuses_another_tenants_upload(strict: FakePaladin) -> None:
    mine = strict.connect(bearer_token=strict.issue_bearer_token(strict.tenant))
    up = mine.data.multipart_upload.initiate_multipart_upload(
        multipart_service_pb2.InitiateMultipartUploadRequest(
            parent=str(strict.collection()), key="k", size_bytes=1
        )
    )
    theirs = strict.connect(bearer_token=strict.issue_bearer_token(str(uuid.uuid4())))
    _refused(
        lambda: theirs.data.multipart_upload.presign_part(
            multipart_service_pb2.PresignPartRequest(
                upload_id=up.upload_id, part_number=1, checksum_value=EMPTY_SHA256
            )
        ),
        UPLOAD_TENANT_MISMATCH,
    )


def test_strict_auth_comes_before_fail_rpc(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    strict.fail_rpc(GET_OBJECT, 1, Code.UNAVAILABLE)
    _refused(lambda: _get(strict.connect(), obj), MISSING_AUTHORIZATION)
    p = strict.connect(bearer_token=strict.issue_bearer_token(strict.tenant))
    with pytest.raises(ConnectError) as caught:
        _get(p, obj)
    assert caught.value.code == Code.UNAVAILABLE


def test_issued_tokens(fake: FakePaladin) -> None:
    tokens = [
        fake.issue_bearer_token(fake.tenant),
        fake.issue_bearer_token(fake.tenant),
        fake.issue_api_token(fake.tenant),
        fake.issue_capability(fake.tenant),
    ]
    assert len(set(tokens)) == len(tokens)
    assert fake.issue_api_token(fake.tenant).startswith("paladin_pat_")


def test_lookup_names_its_parent(strict: FakePaladin) -> None:
    """A parent is a name too: a lookup in another tenant's collection is
    refused."""
    theirs = strict.connect(bearer_token=strict.issue_bearer_token(str(uuid.uuid4())))
    _refused(
        lambda: paladin.lookup_object(theirs.data, ObjectURI(strict.collection(), "k")),
        TENANT_MISMATCH,
    )
