"""paladin.testing seals a real Biscuit, which paladin.attenuate narrows, and
under strict_auth holds a narrowed copy to its narrowing as the server does,
against the table the server and the Go fake are held to
(sdk/testdata/biscuit_attenuations.json)."""

from __future__ import annotations

import importlib.util
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin import Op, Plane, attenuate, biscuit_of
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.common.v1 import error_reason_pb2
from paladin.data.v1 import object_service_pb2, storage_bootstrap_service_pb2
from paladin.errors import limes_code, reason
from paladin.testing import FakePaladin

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "biscuit_attenuations.json"
HAS_BISCUIT = importlib.util.find_spec("biscuit_auth") is not None
needs_biscuit = pytest.mark.skipif(not HAS_BISCUIT, reason="the biscuit extra is not installed")

_PLACEHOLDER_OBJECT = "{object}"
_PLACEHOLDER_ELSEWHERE = "{elsewhere}"
_PLACEHOLDER_COLLECTION = "{collection}"
_ELSEWHERE_COLLECTION = "elsewhere"
_DEFAULT_OPS = (str(Op.GET), str(Op.MANAGE))
_BUCKET = "bucket"
_BACKEND = "b"
# A request ceiling small enough to reach in a test.
_COPY_REQUESTS = 2


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
) -> pb.CapabilityServiceIssueResponse:
    caveats = pb.CapabilityCaveats(
        ops=ops or list(_DEFAULT_OPS),
        resource_prefixes=prefixes or [],
        unlimited_budget=admin_types.UnlimitedBudget(),
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


def _narrow(token: str, blocks: list[dict[str, Any]], filled: Any) -> str:
    for block in blocks:
        args: dict[str, Any] = {}
        if "ops" in block:
            args["ops"] = [Op(o) for o in block["ops"]]
        if "resource_prefixes" in block:
            args["resource_prefixes"] = [filled(p) for p in block["resource_prefixes"]]
        if "planes" in block:
            args["planes"] = [Plane(p) for p in block["planes"]]
        if "expires_in_seconds" in block:
            args["expires_at"] = datetime.now(UTC) + timedelta(seconds=block["expires_in_seconds"])
        token = attenuate(token, **args)
    return token


def _get(p: Any, name: str) -> Any:
    return lambda: p.data.object.get_object(object_service_pb2.GetObjectRequest(name=name))


def _cases() -> list[dict[str, Any]]:
    return json.loads(TABLE.read_text())["cases"]


@needs_biscuit
@pytest.mark.parametrize("case", _cases(), ids=lambda c: c["name"])
def test_a_copy_is_held_as_the_table_says(strict: FakePaladin, case: dict[str, Any]) -> None:
    key = json.loads(TABLE.read_text())["key"]
    obj = strict.put(strict.collection(), key, "text/plain", b"x")
    tenant = strict.tenant
    fill = {
        _PLACEHOLDER_OBJECT: _resource(tenant, strict.collection().collection, key),
        _PLACEHOLDER_ELSEWHERE: _resource(tenant, _ELSEWHERE_COLLECTION, ""),
        _PLACEHOLDER_COLLECTION: _resource(tenant, strict.collection().collection, ""),
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
    )
    p = strict.connect(capability=_narrow(biscuit_of(resp), case["attenuations"], filled))
    calls = {
        "get_object": _get(p, obj.name),
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
        assert _answer(calls[call["call"]]) == want, f"call {i} ({call['call']})"


@needs_biscuit
def test_a_copy_stops_with_its_capability(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    resp = _issue(strict, ops=[str(Op.GET)])
    copy = strict.connect(capability=attenuate(biscuit_of(resp), ops=[Op.GET]))
    admin = strict.connect(bearer_token=strict.issue_bearer_token(strict.tenant)).admin.capability
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=resp.capability.id))
    with pytest.raises(ConnectError) as err:
        _get(copy, obj.name)()
    assert err.value.code == Code.PERMISSION_DENIED


@needs_biscuit
def test_a_copy_spends_its_capabilitys_requests(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    resp = _issue(strict, ops=[str(Op.GET)], max_requests=_COPY_REQUESTS)
    full = strict.connect(capability=biscuit_of(resp))
    copy = strict.connect(capability=attenuate(biscuit_of(resp), ops=[Op.GET]))
    _get(full, obj.name)()
    _get(copy, obj.name)()
    assert _answer(_get(copy, obj.name))[1] == "ERROR_REASON_CAPABILITY_REQUEST_LIMIT_EXCEEDED"


@needs_biscuit
def test_a_copy_cannot_delegate(strict: FakePaladin) -> None:
    resp = _issue(
        strict, audience=[str(Plane.DATA), str(Plane.ADMIN)], ops=[str(Op.GET), str(Op.SHARE)]
    )
    copy = attenuate(biscuit_of(resp), ops=[Op.GET, Op.SHARE])
    with pytest.raises(ConnectError) as err:
        strict.connect(capability=copy).admin.capability.delegate(
            pb.CapabilityServiceDelegateRequest(parent_id=resp.capability.id)
        )
    assert err.value.code == Code.FAILED_PRECONDITION


@pytest.mark.skipif(HAS_BISCUIT, reason="the biscuit extra is installed")
def test_without_the_extra_the_biscuit_is_a_stand_in(strict: FakePaladin) -> None:
    obj = strict.put(strict.collection(), "k", "text/plain", b"x")
    resp = _issue(strict, ops=[str(Op.GET)])
    assert _answer(_get(strict.connect(capability=biscuit_of(resp)), obj.name)) == ("", "", "", "")
