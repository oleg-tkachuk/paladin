"""biscuit_of: the Biscuit an Issue or Delegate answer carries, or why not."""

from __future__ import annotations

import pytest

from paladin import NoBiscuitError, biscuit_of
from paladin.admin.v1 import capability_service_pb2 as pb

SEALED = "biscuit-token"


def test_an_issued_biscuit_is_returned() -> None:
    resp = pb.CapabilityServiceIssueResponse(
        biscuit=SEALED, biscuit_status=pb.BISCUIT_STATUS_ISSUED
    )
    assert biscuit_of(resp) == SEALED


@pytest.mark.parametrize(
    "status",
    [
        pb.BISCUIT_STATUS_NOT_SHAREABLE,
        pb.BISCUIT_STATUS_KEY_BOUND,
        pb.BISCUIT_STATUS_NOT_YET_VALID,
        pb.BISCUIT_STATUS_UNSPECIFIED,
    ],
)
def test_no_biscuit_raises_naming_why(status: int) -> None:
    with pytest.raises(NoBiscuitError) as raised:
        biscuit_of(pb.CapabilityServiceIssueResponse(biscuit_status=status))
    assert raised.value.status == status
    assert pb.BiscuitStatus.Name(status) in str(raised.value)
