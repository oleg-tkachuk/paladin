"""paladin.testing tells a delegation wider than its parent as the server
does: limes's title, then which part of the request was refused — the details
sdk/testdata/delegation_details.json holds to the library, which the Go fake
is held to as well."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

import paladin
from paladin import Op
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.errors import limes_code
from paladin.testing import (
    _DELEGATION_DETAILS,
    _LIMES_DELEGATION_TOO_WIDE,
    _PROBLEM_DETAIL_SEPARATOR,
    _TITLE_DELEGATION_TOO_WIDE,
    FakePaladin,
)

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "delegation_details.json"


def _table() -> dict[str, object]:
    return json.loads(TABLE.read_text())


def test_the_fake_tells_the_tables_details() -> None:
    table = _table()
    assert table["title"] == _TITLE_DELEGATION_TOO_WIDE
    assert table["details"] == _DELEGATION_DETAILS


@pytest.mark.parametrize(
    ("rule", "child"),
    [
        ("op", pb.CapabilityServiceDelegateRequest(caveats=pb.CapabilityCaveats(ops=[Op.PUT]))),
        ("audience", pb.CapabilityServiceDelegateRequest(audience=[str(p) for p in paladin.Plane])),
    ],
)
def test_delegate_tells_what_was_too_wide(rule: str, child: pb.CapabilityServiceDelegateRequest) -> None:
    with FakePaladin(strict_auth=True) as fake:
        admin = fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant)).admin.capability
        parent = admin.issue(
            pb.CapabilityServiceIssueRequest(
                subject=pb.CapabilityPrincipal(
                    kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=fake.tenant, subject="agent:worker"
                ),
                audience=[str(paladin.Plane.DATA), str(paladin.Plane.ADMIN)],
                caveats=pb.CapabilityCaveats(
                    ops=[Op.GET, Op.SHARE],
                    unlimited_requests=True,
                    unlimited_budget=admin_types.UnlimitedBudget(),
                ),
            )
        )
        child.parent_id = parent.capability.id
        with pytest.raises(ConnectError) as refused:
            fake.connect(capability=parent.token).admin.capability.delegate(child)
    assert refused.value.code == Code.PERMISSION_DENIED
    assert limes_code(refused.value) == _LIMES_DELEGATION_TOO_WIDE
    assert refused.value.message == (
        _TITLE_DELEGATION_TOO_WIDE + _PROBLEM_DETAIL_SEPARATOR + _table()["details"][rule]  # type: ignore[index]
    )
