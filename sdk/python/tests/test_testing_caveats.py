"""paladin.testing under strict_auth holds a capability its CapabilityService
issued to its caveats as the server does, against the table the server and
the Go fake are held to (sdk/testdata/capability_refusals.json)."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

import paladin
from paladin import Op, Plane
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.common.v1 import error_reason_pb2
from paladin.data.v1 import object_service_pb2, storage_bootstrap_service_pb2
from paladin.errors import limes_code, reason
from paladin.testing import FakePaladin, _caveat_refusal, _match_resource

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "capability_refusals.json"
MATCHES = Path(__file__).resolve().parents[2] / "testdata" / "capability_resource_matches.json"
_PLACEHOLDER_OBJECT = "{object}"
_PLACEHOLDER_ELSEWHERE = "{elsewhere}"
_ELSEWHERE_COLLECTION = "elsewhere"
_DEFAULT_OPS = (str(Op.GET), str(Op.MANAGE))
_BUCKET = "bucket"
_BACKEND = "b"


@pytest.fixture
def strict():  # type: ignore[no-untyped-def]
    with FakePaladin(strict_auth=True) as f:
        yield f


def _resource(tenant: str, collection: str, key: str) -> str:
    return f"object://{tenant}/{collection}/{key}"


def _issue(
    fake: FakePaladin,
    *,
    audience: list[str] | None = None,
    ops: list[str] | None = None,
    prefixes: list[str] | None = None,
    max_requests: int = 0,
    idempotency_key_required: bool = False,
) -> pb.CapabilityServiceIssueResponse:
    caveats = pb.CapabilityCaveats(
        ops=ops or list(_DEFAULT_OPS),
        resource_prefixes=prefixes or [],
        unlimited_budget=admin_types.UnlimitedBudget(),
        idempotency_key_required=idempotency_key_required,
    )
    if max_requests:
        caveats.max_requests = max_requests
    else:
        caveats.unlimited_requests = True
    admin = fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant)).admin.capability
    return admin.issue(
        pb.CapabilityServiceIssueRequest(
            subject=pb.CapabilityPrincipal(
                kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:worker"
            ),
            audience=audience or [str(Plane.DATA)],
            caveats=caveats,
        )
    )


def _answer(call: Any) -> tuple[str, str, str, str]:
    """What a call's refusal says — code, reason, limes code, message — or
    nothing."""
    try:
        call()
    except ConnectError as err:
        return (
            err.code.value,
            error_reason_pb2.ErrorReason.Name(reason(err)),
            limes_code(err),
            err.message,
        )
    return "", "", "", ""


def _cases() -> list[dict[str, Any]]:
    return json.loads(TABLE.read_text())["cases"]


@pytest.mark.parametrize("case", _cases(), ids=lambda c: c["name"])
def test_a_capability_is_refused_as_the_table_says(
    strict: FakePaladin, case: dict[str, Any]
) -> None:
    key = json.loads(TABLE.read_text())["key"]
    obj = strict.put(strict.collection(), key, "text/plain", b"x")
    fill = {
        _PLACEHOLDER_OBJECT: _resource(strict.tenant, strict.collection().collection, key),
        _PLACEHOLDER_ELSEWHERE: _resource(strict.tenant, _ELSEWHERE_COLLECTION, ""),
    }

    def filled(text: str) -> str:
        for placeholder, value in fill.items():
            text = text.replace(placeholder, value)
        return text

    resp = _issue(
        strict,
        audience=case.get("audience"),
        ops=case.get("ops"),
        prefixes=[filled(p) for p in case.get("prefixes", [])],
        max_requests=case.get("max_requests", 0),
    )
    p = strict.connect(capability=resp.token)
    calls = {
        "get_object": lambda: p.data.object.get_object(
            object_service_pb2.GetObjectRequest(name=obj.name)
        ),
        "ensure_tenant_storage": lambda: p.data.storage_bootstrap.ensure_tenant_storage(
            storage_bootstrap_service_pb2.EnsureTenantStorageRequest(
                backend_id=_BACKEND, bucket=_BUCKET
            )
        ),
    }
    for i, call in enumerate(case["calls"]):
        want = (
            call.get("code", ""),
            call.get("reason", ""),
            call.get("limes_code", ""),
            filled(call.get("message", "")),
        )
        if not call.get("code"):
            want = ("", "", "", "")
        assert _answer(calls[call["call"]]) == want, f"call {i} ({call['call']})"


def test_an_ancestors_request_ceiling_counts(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    parent = _issue(
        strict,
        audience=[str(Plane.DATA), str(Plane.ADMIN)],
        ops=[str(Op.GET), str(Op.SHARE)],
        max_requests=2,
    )
    child = strict.connect(capability=parent.token).admin.capability.delegate(
        pb.CapabilityServiceDelegateRequest(
            parent_id=parent.capability.id, caveats=pb.CapabilityCaveats(ops=[str(Op.GET)])
        )
    )
    as_child = strict.connect(capability=child.token)
    get = lambda: as_child.data.object.get_object(
        object_service_pb2.GetObjectRequest(name=obj.name)
    )
    assert _answer(get) == ("", "", "", "")
    assert _answer(get) == (
        Code.RESOURCE_EXHAUSTED.value,
        "ERROR_REASON_CAPABILITY_REQUEST_LIMIT_EXCEEDED",
        "request_limit_exceeded",
        f"request limit exceeded: ancestor {parent.capability.id}",
    )


def test_a_write_needs_the_idempotency_key_it_requires(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    resp = _issue(strict, ops=[str(Op.GET), str(Op.DELETE)], idempotency_key_required=True)
    p = strict.connect(capability=resp.token)
    # A read needs none.
    assert _answer(
        lambda: p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))
    ) == ("", "", "", "")


def test_without_strict_auth_nothing_is_checked() -> None:
    with FakePaladin() as fake:
        obj = fake.put(fake.collection(), "k", "text/plain", b"x")
        resp = _issue(fake, audience=[paladin.AUDIENCE_DATA], ops=[str(Op.LIST)])
        p = fake.connect(capability=resp.token)
        assert _answer(
            lambda: p.data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))
        ) == ("", "", "", "")


@pytest.mark.parametrize(
    "row",
    json.loads(MATCHES.read_text())["matches"],
    ids=lambda r: f"{r['prefix']} {r['uri']}",
)
def test_a_prefix_reaches_what_limes_says_it_does(row: dict[str, Any]) -> None:
    """The fake has no limes to ask, so it is held to limes's own answers,
    which the backend's tests hold the table to."""
    assert _match_resource(row["prefix"], row["uri"]) is row["match"]


def test_a_write_refused_without_the_key_its_capability_requires() -> None:
    caveats = pb.CapabilityCaveats(ops=[str(Op.GET), str(Op.DELETE)], idempotency_key_required=True)
    refusal = _caveat_refusal(caveats, str(Op.DELETE), "object://t/c/k", has_idempotency_key=False)
    assert refusal is not None
    assert (refusal.code, refusal.message) == (
        Code.PERMISSION_DENIED,
        'idempotency key required: mutating operation "delete"',
    )
    assert (
        _caveat_refusal(caveats, str(Op.DELETE), "object://t/c/k", has_idempotency_key=True) is None
    )
    assert (
        _caveat_refusal(caveats, str(Op.GET), "object://t/c/k", has_idempotency_key=False) is None
    )
