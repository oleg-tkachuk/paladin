"""biscuit_of: the Biscuit an Issue or Delegate answer carries, or why not."""

from __future__ import annotations

import pytest

from paladin import (
    Credential,
    CredentialForm,
    NoBiscuitError,
    NoCredentialError,
    biscuit_of,
    credential_of,
)
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


TOKEN = "jwt-token"


@pytest.mark.parametrize(
    ("resp", "want"),
    [
        (
            pb.CapabilityServiceIssueResponse(
                token=TOKEN, biscuit=SEALED, biscuit_status=pb.BISCUIT_STATUS_ISSUED
            ),
            Credential(SEALED, CredentialForm.BISCUIT, pb.BISCUIT_STATUS_ISSUED),
        ),
        (
            pb.CapabilityServiceIssueResponse(
                token=TOKEN, biscuit_status=pb.BISCUIT_STATUS_KEY_BOUND
            ),
            Credential(TOKEN, CredentialForm.TOKEN, pb.BISCUIT_STATUS_KEY_BOUND),
        ),
        (
            pb.CapabilityServiceIssueResponse(
                token=TOKEN, biscuit_status=pb.BISCUIT_STATUS_NOT_YET_VALID
            ),
            Credential(TOKEN, CredentialForm.TOKEN, pb.BISCUIT_STATUS_NOT_YET_VALID),
        ),
        # A server too old to name a status: a Biscuit beside it is not
        # trusted to be one, and the token is presented.
        (
            pb.CapabilityServiceIssueResponse(token=TOKEN, biscuit=SEALED),
            Credential(TOKEN, CredentialForm.TOKEN, pb.BISCUIT_STATUS_UNSPECIFIED),
        ),
    ],
)
def test_credential_of_presents_the_biscuit_or_the_token(
    resp: pb.CapabilityServiceIssueResponse, want: Credential
) -> None:
    assert credential_of(resp) == want


def test_credential_of_an_empty_answer_raises() -> None:
    with pytest.raises(NoCredentialError) as raised:
        credential_of(pb.CapabilityServiceIssueResponse())
    assert raised.value.status == pb.BISCUIT_STATUS_UNSPECIFIED
