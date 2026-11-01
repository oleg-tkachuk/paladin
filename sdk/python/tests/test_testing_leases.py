"""paladin.testing serves the lease calls as the server answers them, against
the table the server and the Go fake are held to
(sdk/testdata/capability_leases.json)."""

from __future__ import annotations

import importlib.util
import json
import uuid
from pathlib import Path
from typing import Any

import pytest
from connectrpc.errors import ConnectError

from paladin import Op, Plane, attenuate
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.errors import limes_code
from paladin.testing import FakePaladin

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "capability_leases.json"
HAS_BISCUIT = importlib.util.find_spec("biscuit_auth") is not None

_UNKNOWN = "unknown"
_COPY = "copy"
_BISCUIT = "biscuit"
_DELEGATE_TTL_SECONDS = 600


def _cases() -> list[dict[str, Any]]:
    return json.loads(TABLE.read_text())["cases"]


def _caveats(*ops: Op) -> pb.CapabilityCaveats:
    return pb.CapabilityCaveats(
        ops=[str(o) for o in ops],
        unlimited_requests=True,
        unlimited_budget=admin_types.UnlimitedBudget(),
    )


def _chain(fake: FakePaladin) -> tuple[dict[str, str], dict[str, str]]:
    """The table's roles, issued through the fake's own CapabilityService:
    each role's id and the token it presents."""
    admin = fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant)).admin.capability

    def root(*ops: Op) -> pb.CapabilityServiceIssueResponse:
        return admin.issue(
            pb.CapabilityServiceIssueRequest(
                subject=pb.CapabilityPrincipal(
                    kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:lease"
                ),
                audience=[str(Plane.DATA), str(Plane.ADMIN)],
                caveats=_caveats(*ops),
            )
        )

    def delegate(
        parent: pb.CapabilityServiceIssueResponse, *ops: Op
    ) -> pb.CapabilityServiceIssueResponse:
        return fake.connect(capability=parent.token).admin.capability.delegate(
            pb.CapabilityServiceDelegateRequest(
                parent_id=parent.capability.id,
                ttl_seconds=_DELEGATE_TTL_SECONDS,
                caveats=_caveats(*ops),
            )
        )

    holder = root(Op.GET, Op.SHARE)
    child = delegate(holder, Op.GET, Op.SHARE)
    sibling = delegate(holder, Op.GET, Op.SHARE)
    grandchild = delegate(child, Op.GET)
    revoked = delegate(holder, Op.GET)
    admin.revoke(pb.CapabilityServiceRevokeRequest(id=revoked.capability.id))
    stranger = root(Op.GET)
    roles = {
        "holder": holder,
        "child": child,
        "sibling": sibling,
        "grandchild": grandchild,
        "revoked": revoked,
        "stranger": stranger,
    }
    ids = {name: r.capability.id for name, r in roles.items()}
    tokens = {name: r.token for name, r in roles.items()}
    tokens[_BISCUIT] = holder.biscuit
    if HAS_BISCUIT:
        tokens[_COPY] = attenuate(holder.biscuit, ops=[Op.GET, Op.SHARE])
    return ids, tokens


def _answer(call: Any) -> tuple[str, str, int]:
    """What a step got: its refusal's code and limes code, or the requests
    its lease holds."""
    try:
        res = call()
    except ConnectError as err:
        return err.code.value, limes_code(err), 0
    return "", "", res.lease.requests if hasattr(res, "lease") else 0


def _want(step: dict[str, Any]) -> tuple[str, str, int]:
    if step.get("code"):
        return step["code"], step.get("limes_code", ""), 0
    return "", "", step.get("held", 0)


@pytest.mark.parametrize("case", _cases(), ids=lambda c: c["name"])
def test_a_lease_call_answers_as_the_table_says(case: dict[str, Any]) -> None:
    if case["as"] == _COPY and not HAS_BISCUIT:
        pytest.skip("the biscuit extra is not installed")
    with FakePaladin() as fake:
        ids, tokens = _chain(fake)
        operator = fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant)).admin.capability
        if case["as"]:
            client = fake.connect(capability=tokens[case["as"]]).admin.capability
        else:
            client = fake.connect(
                bearer_token=fake.issue_bearer_token(fake.tenant)
            ).admin.capability
        for i, step in enumerate(case["steps"]):
            target = str(uuid.uuid4()) if step["of"] == _UNKNOWN else ids[step["of"]]
            calls = {
                "revoke": lambda t=target: operator.revoke(pb.CapabilityServiceRevokeRequest(id=t)),
                "lease": lambda t=target, s=step: client.lease(
                    pb.CapabilityServiceLeaseRequest(id=t, requests=s.get("requests", 0))
                ),
                "get_lease": lambda t=target: client.get_lease(
                    pb.CapabilityServiceGetLeaseRequest(id=t)
                ),
                "settle_lease": lambda t=target: client.settle_lease(
                    pb.CapabilityServiceSettleLeaseRequest(id=t)
                ),
            }
            assert _answer(calls[step["call"]]) == _want(step), (
                f"step {i} ({step['call']} of {step['of']})"
            )
