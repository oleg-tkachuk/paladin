"""paladin.testing under strict_auth refuses a capability it did not issue
with the server's code and reason, against the table the server and the Go
fake are held to (sdk/testdata/unknown_capabilities.json). Its message is the
fake's own: the server's is its library's, often in Go's wording."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from connectrpc.errors import ConnectError

from paladin import Op, Plane, biscuit_of
from paladin.admin.v1 import capability_service_pb2 as pb
from paladin.admin.v1 import types_pb2 as admin_types
from paladin.common.v1 import error_reason_pb2
from paladin.data.v1 import object_service_pb2
from paladin.errors import reason
from paladin.testing import FakePaladin

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "unknown_capabilities.json"
_FOREIGN_TOKEN = "{foreign_token}"
_FOREIGN_BISCUIT = "{foreign_biscuit}"


def _foreign() -> dict[str, str]:
    """A capability and its Biscuit from another fake: an issuer this one
    does not trust."""
    with FakePaladin() as other:
        admin = other.connect(bearer_token=other.issue_bearer_token(other.tenant)).admin.capability
        resp = admin.issue(
            pb.CapabilityServiceIssueRequest(
                subject=pb.CapabilityPrincipal(
                    kind=pb.PRINCIPAL_KIND_AGENT, tenant_id=other.tenant, subject="agent:x"
                ),
                audience=[str(Plane.DATA)],
                caveats=pb.CapabilityCaveats(
                    ops=[str(Op.GET)],
                    unlimited_requests=True,
                    unlimited_budget=admin_types.UnlimitedBudget(),
                ),
            )
        )
    return {_FOREIGN_TOKEN: resp.token, _FOREIGN_BISCUIT: biscuit_of(resp)}


@pytest.mark.parametrize("case", json.loads(TABLE.read_text())["cases"], ids=lambda c: c["name"])
def test_an_unknown_capability_is_refused_as_the_table_says(case: dict[str, Any]) -> None:
    token = _foreign().get(case["token"], case["token"])
    with FakePaladin(strict_auth=True) as fake:
        obj = fake.put(fake.collection(), "k", "text/plain", b"x")
        with pytest.raises(ConnectError) as err:
            fake.connect(capability=token).data.object.get_object(
                object_service_pb2.GetObjectRequest(name=obj.name)
            )
    assert err.value.code.value == case["code"]
    assert error_reason_pb2.ErrorReason.Name(reason(err.value)) == case["reason"]
