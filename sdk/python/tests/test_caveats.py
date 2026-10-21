"""Caveats Issue takes, checked before they are sent."""

from __future__ import annotations

import pytest

from paladin import Op, unlimited_caveats, validate_issue_caveats


def test_unlimited_caveats_pass_the_check() -> None:
    caveats = unlimited_caveats(Op.GET, Op.LIST)
    assert list(caveats.ops) == ["get", "list"]
    assert caveats.unlimited_requests
    assert caveats.HasField("unlimited_budget")
    assert caveats.unlimited_budget.currency_code == ""
    validate_issue_caveats(caveats)


def test_a_limit_of_zero_is_a_limit() -> None:
    caveats = unlimited_caveats(Op.GET)
    caveats.max_requests = 0
    validate_issue_caveats(caveats)


@pytest.mark.parametrize(
    ("edit", "names"),
    [
        (lambda c: c.ClearField("ops"), ["caveats.ops"]),
        (lambda c: c.ops.__setitem__(0, "presign"), ["caveats.ops", "unknown op"]),
        (lambda c: c.ClearField("unlimited_requests"), ["caveats.request_ceiling"]),
        (lambda c: c.ClearField("unlimited_budget"), ["caveats.budget_ceiling"]),
    ],
    ids=["no ops", "a bare op", "no request limit", "no budget limit"],
)
def test_what_issue_refuses_is_refused_here(edit, names) -> None:  # type: ignore[no-untyped-def]
    caveats = unlimited_caveats(Op.GET)
    edit(caveats)
    with pytest.raises(ValueError) as err:
        validate_issue_caveats(caveats)
    for name in names:
        assert name in str(err.value)
