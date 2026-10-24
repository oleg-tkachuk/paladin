"""paladin.testing's CapabilityService, to the server's rules — the same
scenarios the Go fake's tests run, and the operation names both SDKs are held
to (sdk/testdata/capability_op_names.json)."""

from __future__ import annotations

import json
import time
import uuid
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf import timestamp_pb2

import paladin
from paladin import CredentialForm, NoBiscuitError, Op, biscuit_of, credential_of
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.caveats import op_refusal
from paladin.common.v1.pagination_pb2 import SORT_ORDER_ASC, SORT_ORDER_DESC
from paladin.data.v1 import object_service_pb2, types_pb2
from paladin.testing import FakePaladin

OP_NAMES = Path(__file__).resolve().parents[2] / "testdata" / "capability_op_names.json"
JKT = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"
"""A JWK thumbprint to bind a capability to: its shape is all the server checks."""
ISSUE = "/paladin.admin.v1.CapabilityService/Issue"


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


@pytest.fixture
def strict():  # type: ignore[no-untyped-def]
    with FakePaladin(strict_auth=True) as f:
        yield f


def _issue_request(fake: FakePaladin, *ops: str) -> pb.CapabilityServiceIssueRequest:
    return pb.CapabilityServiceIssueRequest(
        subject=pb.CapabilityPrincipal(
            kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:worker"
        ),
        audience=[paladin.AUDIENCE_DATA],
        caveats=pb.CapabilityCaveats(
            ops=list(ops), unlimited_requests=True, unlimited_budget=admin_types.UnlimitedBudget()
        ),
    )


def _admin(fake: FakePaladin) -> paladin.Paladin:
    return fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant))


def _get(p: paladin.Paladin, obj: types_pb2.Object) -> None:
    p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))


def _in(delta: timedelta) -> timestamp_pb2.Timestamp:
    ts = timestamp_pb2.Timestamp()
    ts.FromDatetime(datetime.now(UTC) + delta)
    return ts


def _code(call) -> Code:  # type: ignore[no-untyped-def]
    with pytest.raises(ConnectError) as caught:
        call()
    return caught.value.code


def test_the_op_names_are_limes_rule() -> None:
    names = json.loads(OP_NAMES.read_text())
    for op in names["valid"]:
        assert op_refusal(op) is None, op
    for op in names["invalid"]:
        assert op_refusal(op) is not None, op


def test_a_capability_without_share_has_a_biscuit_that_is_let_through(
    strict: FakePaladin,
) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    resp = _admin(strict).admin.capability.issue(_issue_request(strict, Op.GET, Op.LIST))
    assert resp.biscuit_status == pb.BISCUIT_STATUS_ISSUED
    assert resp.biscuit and resp.token
    assert credential_of(resp).form == CredentialForm.BISCUIT
    for value in (resp.biscuit, resp.token):
        _get(strict.connect(capability=value), obj)
    assert resp.capability.subject.tenant_id == strict.tenant
    assert resp.capability.HasField("expires_at")


@pytest.mark.parametrize(
    ("mutate", "status"),
    [
        (lambda r: setattr(r, "confirmation_jkt", JKT), pb.BISCUIT_STATUS_KEY_BOUND),
        (
            lambda r: r.not_before.CopyFrom(_in(timedelta(minutes=1))),
            pb.BISCUIT_STATUS_NOT_YET_VALID,
        ),
    ],
)
def test_names_why_there_is_no_biscuit(fake: FakePaladin, mutate, status: int) -> None:  # type: ignore[no-untyped-def]
    admin = fake.connect().admin.capability
    req = _issue_request(fake, Op.GET)
    mutate(req)
    resp = admin.issue(req)
    assert resp.biscuit_status == status and not resp.biscuit
    cred = credential_of(resp)
    assert (cred.form, cred.value) == (CredentialForm.TOKEN, resp.token)
    with pytest.raises(NoBiscuitError):
        biscuit_of(resp)
    req.require_biscuit = True
    with pytest.raises(ConnectError) as caught:
        admin.issue(req)
    assert caught.value.code == Code.FAILED_PRECONDITION
    assert pb.BiscuitStatus.Name(status) in str(caught.value)


@pytest.mark.parametrize(
    ("ops", "drop", "names"),
    [
        ((Op.GET, "presign"), None, '"presign"'),
        (("Get",), None, '"Get"'),
        # The contract's own rule, which protovalidate holds.
        ((), None, "caveats.ops"),
        ((Op.GET,), "request_ceiling", "request_ceiling"),
        ((Op.GET,), "budget_ceiling", "budget_ceiling"),
    ],
)
def test_issue_refuses_what_the_server_refuses(
    fake: FakePaladin, ops: tuple[str, ...], drop: str | None, names: str
) -> None:
    req = _issue_request(fake, *ops)
    if drop is not None:
        req.caveats.ClearField(drop)
    with pytest.raises(ConnectError) as caught:
        fake.connect().admin.capability.issue(req)
    assert caught.value.code == Code.INVALID_ARGUMENT
    assert names in str(caught.value)


def test_strict_issue_stays_in_the_callers_tenant(strict: FakePaladin) -> None:
    req = _issue_request(strict, Op.GET)
    req.subject.tenant_id = str(uuid.uuid4())
    assert _code(lambda: _admin(strict).admin.capability.issue(req)) == Code.PERMISSION_DENIED
    own = _issue_request(strict, Op.GET)
    assert _code(lambda: strict.connect().admin.capability.issue(own)) == Code.UNAUTHENTICATED


def _narrow(parent_id: str, *ops: str) -> pb.CapabilityServiceDelegateRequest:
    return pb.CapabilityServiceDelegateRequest(
        parent_id=parent_id, caveats=pb.CapabilityCaveats(ops=list(ops))
    )


def test_delegate(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    admin = _admin(strict).admin.capability
    parent = admin.issue(_issue_request(strict, Op.GET, Op.LIST, Op.SHARE))
    no_share = admin.issue(_issue_request(strict, Op.GET))
    pid = parent.capability.id

    def as_(token: str):  # type: ignore[no-untyped-def]
        return strict.connect(capability=token).admin.capability

    child = as_(parent.token).delegate(_narrow(pid, Op.GET))
    assert child.biscuit_status == pb.BISCUIT_STATUS_ISSUED
    assert child.capability.parent_id == pid
    _get(strict.connect(capability=child.biscuit), obj)

    bound = _narrow(pid, Op.GET)
    bound.confirmation_jkt, bound.require_biscuit = JKT, True
    for client, req, code in [
        (as_(no_share.token), _narrow(no_share.capability.id, Op.GET), Code.PERMISSION_DENIED),
        (as_(parent.token), _narrow(no_share.capability.id, Op.GET), Code.PERMISSION_DENIED),
        (as_(parent.biscuit), _narrow(pid, Op.GET), Code.FAILED_PRECONDITION),
        (as_(parent.token), _narrow(pid, Op.PUT), Code.PERMISSION_DENIED),
        (as_(parent.token), _narrow(pid, "presign"), Code.INVALID_ARGUMENT),
        (_admin(strict).admin.capability, _narrow(pid, Op.GET), Code.FAILED_PRECONDITION),
        (as_(parent.token), bound, Code.FAILED_PRECONDITION),
    ]:
        assert _code(lambda c=client, r=req: c.delegate(r)) == code, req


def test_revoke_and_get(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    admin = _admin(strict).admin.capability
    parent = admin.issue(_issue_request(strict, Op.GET, Op.SHARE))
    child = strict.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(parent_id=parent.capability.id)
    )
    got = admin.get(pb.CapabilityServiceGetRequest(id=parent.capability.id))
    assert got.capability.id == parent.capability.id
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id, cascade_children=True))
    for token in (parent.token, parent.biscuit, child.token):
        assert (
            _code(lambda t=token: _get(strict.connect(capability=t), obj)) == Code.PERMISSION_DENIED
        )
    unknown = str(uuid.uuid4())
    assert (
        _code(lambda: admin.revoke(pb.CapabilityServiceRevokeRequest(id=unknown))) == Code.NOT_FOUND
    )
    assert _code(lambda: admin.get(pb.CapabilityServiceGetRequest(id=unknown))) == Code.NOT_FOUND


def test_faults_and_gaps(fake: FakePaladin) -> None:
    admin = fake.connect().admin.capability
    fake.fail_rpc(ISSUE, 1, Code.UNAVAILABLE)
    assert _code(lambda: admin.issue(_issue_request(fake, Op.GET))) == Code.UNAVAILABLE
    usage = pb.CapabilityServiceGetUsageRequest(id=str(uuid.uuid4()))
    assert _code(lambda: admin.get_usage(usage)) == Code.UNIMPLEMENTED


EXPIRING_TTL_SECONDS = 1
"""The lifetime of the capability the listing test waits out, so it lists one
expired."""
_AGENT, _USER = pb.PRINCIPAL_KIND_AGENT, pb.PRINCIPAL_KIND_USER
_ALL = [pb.CAPABILITY_DISPOSITION_ACTIVE, pb.CAPABILITY_DISPOSITION_EXPIRED, pb.CAPABILITY_DISPOSITION_REVOKED]
_A, _E, _R = _ALL


@pytest.fixture(scope="module")
def listed():  # type: ignore[no-untyped-def]
    """The scenario capabilityh/sdk_fake_fidelity_test.go lists through the
    server's handler and the Go fake: the same principals, one revoked and
    one expired."""
    with FakePaladin() as f:
        admin = f.connect().admin.capability
        expires = datetime.now(UTC)
        for kind, subject, ttl, revoke in [
            (_AGENT, "agent:one", 300, False),
            (_AGENT, "agent:two", 300, True),
            (_USER, "user:one", 300, False),
            (_USER, "user:gone", EXPIRING_TTL_SECONDS, False),
            (_AGENT, "agent:one", 300, False),
        ]:
            req = _issue_request(f, Op.GET)
            req.subject.kind, req.subject.subject, req.ttl_seconds = kind, subject, ttl
            got = admin.issue(req)
            if ttl == EXPIRING_TTL_SECONDS:
                expires = got.capability.expires_at.ToDatetime(tzinfo=UTC)
            if revoke:
                admin.revoke(pb.CapabilityServiceRevokeRequest(id=got.capability.id))
        time.sleep(max((expires - datetime.now(UTC)).total_seconds(), 0) + 0.001)
        yield f


def _list_all(fake: FakePaladin, **fields) -> list[tuple[int, str, int]]:  # type: ignore[no-untyped-def]
    admin = fake.connect().admin.capability
    out, token = [], ""
    while True:
        resp = admin.list(pb.CapabilityServiceListRequest(tenant_id=fake.tenant, page_token=token, **fields))
        out += [(c.subject.kind, c.subject.subject, c.disposition) for c in resp.capabilities]
        token = resp.next_page_token
        if not token:
            return out


_ASC = [
    (_AGENT, "agent:one", _A),
    (_AGENT, "agent:two", _R),
    (_USER, "user:one", _A),
    (_USER, "user:gone", _E),
    (_AGENT, "agent:one", _A),
]


@pytest.mark.parametrize(
    ("fields", "want"),
    [
        ({}, [c for c in _ASC if c[2] == _A]),
        ({"dispositions": _ALL, "sort_order": SORT_ORDER_DESC}, _ASC[::-1]),
        ({"principal_kind": _AGENT, "dispositions": _ALL}, [c for c in _ASC if c[0] == _AGENT]),
        ({"subject": "agent:one", "dispositions": _ALL}, [c for c in _ASC if c[1] == "agent:one"]),
        ({"dispositions": [_R]}, [c for c in _ASC if c[2] == _R]),
        ({"include_expired": True, "include_revoked": True}, _ASC),
        ({"dispositions": _ALL, "page_size": 2}, _ASC),
        ({"dispositions": _ALL, "page_size": 2, "sort_order": SORT_ORDER_DESC}, _ASC[::-1]),
    ],
    ids=[
        "the tenant alone",
        "every disposition, newest first",
        "a kind",
        "a subject",
        "revoked",
        "the include flags",
        "a page at a time",
        "a page at a time, newest first",
    ],
)
def test_list_as_the_server_does(listed: FakePaladin, fields, want) -> None:  # type: ignore[no-untyped-def]
    assert _list_all(listed, **fields) == want


@pytest.mark.parametrize("order", [SORT_ORDER_ASC, SORT_ORDER_DESC], ids=["ascending", "descending"])
def test_list_refuses_a_token_of_the_other_order(listed: FakePaladin, order: int) -> None:
    admin = listed.connect().admin.capability
    first = admin.list(
        pb.CapabilityServiceListRequest(tenant_id=listed.tenant, dispositions=_ALL, page_size=1, sort_order=order)
    )
    other = SORT_ORDER_DESC if order == SORT_ORDER_ASC else SORT_ORDER_ASC
    again = pb.CapabilityServiceListRequest(
        tenant_id=listed.tenant, sort_order=other, page_token=first.next_page_token
    )
    assert _code(lambda: admin.list(again)) == Code.INVALID_ARGUMENT
    garbage = pb.CapabilityServiceListRequest(tenant_id=listed.tenant, page_token="garbage")
    assert _code(lambda: admin.list(garbage)) == Code.INVALID_ARGUMENT


@pytest.mark.parametrize(
    "other",
    [{"principal_kind": _USER}, {"subject": "agent:one"}, {"dispositions": [_A]}],
    ids=["another kind", "another subject", "other dispositions"],
)
def test_list_refuses_a_token_of_other_filters(listed: FakePaladin, other) -> None:  # type: ignore[no-untyped-def]
    admin = listed.connect().admin.capability
    first = admin.list(pb.CapabilityServiceListRequest(tenant_id=listed.tenant, dispositions=_ALL, page_size=1))
    fields = {"dispositions": _ALL, **other}
    again = pb.CapabilityServiceListRequest(tenant_id=listed.tenant, page_token=first.next_page_token, **fields)
    assert _code(lambda: admin.list(again)) == Code.INVALID_ARGUMENT


def test_list_counts_a_child_of_a_revoked_parent_revoked(fake: FakePaladin) -> None:
    admin = fake.connect().admin.capability
    parent = admin.issue(_issue_request(fake, Op.GET, Op.SHARE))
    child = fake.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(parent_id=parent.capability.id)
    )
    elsewhere = _issue_request(fake, Op.GET)
    elsewhere.subject.tenant_id = str(uuid.uuid4())
    admin.issue(elsewhere)
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id))
    revoked = admin.list(pb.CapabilityServiceListRequest(tenant_id=fake.tenant, dispositions=[_R]))
    assert [c.id for c in revoked.capabilities] == [parent.capability.id, child.capability.id]
    assert not admin.list(pb.CapabilityServiceListRequest(tenant_id=fake.tenant)).capabilities


def test_strict_list_stays_in_the_callers_tenant(strict: FakePaladin) -> None:
    admin = _admin(strict).admin.capability
    admin.list(pb.CapabilityServiceListRequest(tenant_id=strict.tenant))
    other = pb.CapabilityServiceListRequest(tenant_id=str(uuid.uuid4()))
    assert _code(lambda: admin.list(other)) == Code.PERMISSION_DENIED


def test_get_gives_the_disposition_as_the_server_does(fake: FakePaladin) -> None:
    """The cases capabilityh/sdk_fake_fidelity_test.go holds the Go fake's Get
    to: active, revoked itself, and revoked only through its parent."""
    admin = fake.connect().admin.capability
    active = admin.issue(_issue_request(fake, Op.GET))
    parent = admin.issue(_issue_request(fake, Op.GET, Op.SHARE))
    child = fake.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(parent_id=parent.capability.id)
    )
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id))

    def disposition(c: pb.Capability) -> int:
        got = admin.get(pb.CapabilityServiceGetRequest(id=c.id)).capability
        return got.disposition

    assert disposition(active.capability) == _A
    assert disposition(parent.capability) == _R
    assert disposition(child.capability) == _R


def test_revoke_says_what_it_did_and_get_what_stopped_it(fake: FakePaladin) -> None:
    """The cases capabilityh/sdk_fake_fidelity_test.go holds the Go fake to: a
    second revoke keeps the first entry, each says what the capability was
    before it, and a child names its parent's."""
    admin = fake.connect().admin.capability
    parent = admin.issue(_issue_request(fake, Op.GET, Op.SHARE))
    child = fake.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(parent_id=parent.capability.id)
    )
    first = admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id, reason="leaked"))
    assert not first.already_revoked
    assert first.before == _A
    again = admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id, reason="again"))
    assert again.already_revoked
    assert again.before == _R
    assert again.revocation.reason == "leaked"

    got = admin.get(pb.CapabilityServiceGetRequest(id=child.capability.id))
    assert not got.HasField("revocation")
    assert got.stopped_by.capability_id == parent.capability.id
    assert got.stopped_by.revocation.reason == "leaked"
    own = admin.get(pb.CapabilityServiceGetRequest(id=parent.capability.id))
    assert own.stopped_by.capability_id == parent.capability.id


def test_revoke_of_a_child_stopped_through_its_parent_stops_nothing_new(fake: FakePaladin) -> None:
    """A child stopped only through its revoked parent has no entry of its own:
    its revoke writes one, but says it was revoked already before the call."""
    admin = fake.connect().admin.capability
    parent = admin.issue(_issue_request(fake, Op.GET, Op.SHARE))
    child = fake.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(parent_id=parent.capability.id)
    )
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=parent.capability.id))
    got = admin.revoke(pb.CapabilityServiceRevokeRequest(id=child.capability.id))
    assert not got.already_revoked
    assert got.before == _R
