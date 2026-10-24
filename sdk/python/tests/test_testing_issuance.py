"""paladin.testing's Issue and Delegate, against the table the server and the
Go fake are held to (sdk/testdata/capability_issuance.json)."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pytest
from connectrpc.errors import ConnectError
from google.protobuf import timestamp_pb2

from paladin import Plane
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.testing import FakePaladin

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "capability_issuance.json"
JKT = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"
"""A JWK thumbprint to bind a capability to: its shape is all the server checks."""
TTL_SECONDS = 300
"""The lifetime an issue case asks for, which "past_expiry" lies beyond."""
DELEGATE_TTL_SECONDS = 60
DEFAULT_OPS = ("get", "list")
NOT_BEFORE = {"later": timedelta(minutes=1), "past_expiry": timedelta(hours=1)}

_TABLE = json.loads(TABLE.read_text())


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def _caveats(ops: list[str]) -> pb.CapabilityCaveats:
    return pb.CapabilityCaveats(
        ops=ops, unlimited_requests=True, unlimited_budget=admin_types.UnlimitedBudget()
    )


def _in(delta: timedelta) -> timestamp_pb2.Timestamp:
    ts = timestamp_pb2.Timestamp()
    ts.FromDatetime(datetime.now(UTC) + delta)
    return ts


def _answer(call: Any) -> dict[str, Any]:
    """What Issue or Delegate answered, as the table spells it."""
    try:
        resp = call()
    except ConnectError as err:
        return {"code": err.code.value}
    return {
        "biscuit_status": pb.BiscuitStatus.Name(resp.biscuit_status),
        "biscuit": bool(resp.biscuit),
    }


@pytest.mark.parametrize("case", _TABLE["issue"], ids=lambda c: c["name"])
def test_issue_answers_as_the_table_says(fake: FakePaladin, case: dict[str, Any]) -> None:
    caveats = _caveats(case.get("ops", list(DEFAULT_OPS)))
    if case.get("no_request_ceiling"):
        caveats.ClearField("request_ceiling")
    if case.get("no_budget_ceiling"):
        caveats.ClearField("budget_ceiling")
    req = pb.CapabilityServiceIssueRequest(
        subject=pb.CapabilityPrincipal(
            kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:worker"
        ),
        audience=[str(Plane.DATA)],
        ttl_seconds=TTL_SECONDS,
        caveats=caveats,
        require_biscuit=case.get("require_biscuit", False),
    )
    if case.get("key_bound"):
        req.confirmation_jkt = JKT
    if "not_before" in case:
        req.not_before.CopyFrom(_in(NOT_BEFORE[case["not_before"]]))
    assert _answer(lambda: fake.connect().admin.capability.issue(req)) == case["want"]


@pytest.mark.parametrize("case", _TABLE["delegate"], ids=lambda c: c["name"])
def test_delegate_answers_as_the_table_says(fake: FakePaladin, case: dict[str, Any]) -> None:
    parent = fake.connect().admin.capability.issue(
        pb.CapabilityServiceIssueRequest(
            subject=pb.CapabilityPrincipal(
                kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:parent"
            ),
            audience=[str(Plane.DATA), str(Plane.ADMIN)],
            caveats=_caveats(case["parent_ops"]),
        )
    )
    req = pb.CapabilityServiceDelegateRequest(
        parent_id=parent.capability.id,
        ttl_seconds=DELEGATE_TTL_SECONDS,
        require_biscuit=case.get("require_biscuit", False),
    )
    if "child_ops" in case:
        req.caveats.CopyFrom(_caveats(case["child_ops"]))
    if case.get("key_bound"):
        req.confirmation_jkt = JKT
    delegate = fake.connect(capability=parent.token).admin.capability.delegate
    assert _answer(lambda: delegate(req)) == case["want"]
