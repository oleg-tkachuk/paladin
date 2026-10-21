"""Caveats ``CapabilityService.Issue`` takes, checked before they are sent.

Issue refuses caveats that name no operation, name one the server does not
know, or leave the request or the budget ceiling unset. ``unlimited_caveats``
is the least it takes; ``validate_issue_caveats`` raises what it would refuse,
where the caveats are written rather than after a round trip.
"""

from __future__ import annotations

import json

from paladin.admin.v1 import capability_service_pb2, types_pb2
from paladin.auth import Op

BUILTIN_OPS = (Op.GET, Op.PUT, Op.LIST, Op.DELETE, Op.SHARE, Op.MANAGE)
"""limes's own operations; any other must be namespaced, as ``ns:name``."""
_OP_NAMESPACE = ":"
_ASCII_PRINTABLE_FIRST = 0x21
_ASCII_PRINTABLE_LAST = 0x7E
"""The printable ASCII characters but the space: what a namespaced op holds."""

# The oneofs Issue requires, and the server's answers when one is unset.
_REQUEST_CEILING = "request_ceiling"
_BUDGET_CEILING = "budget_ceiling"
NO_OPS = "caveats.ops: name at least one operation"
NO_REQUEST_CEILING = "caveats.request_ceiling: set max_requests, or unlimited_requests for no limit"
NO_BUDGET_CEILING = "caveats.budget_ceiling: set max_budget, or unlimited_budget for no limit"


def op_refusal(op: str) -> str | None:
    """The server's refusal of ``op`` in a caveat, None when it accepts it:
    limes's rule, which sdk/testdata/capability_op_names.json holds both SDKs
    to."""
    if op in BUILTIN_OPS:
        return None
    ns, sep, name = op.partition(_OP_NAMESPACE)
    if not sep or not ns or not name:
        builtin = ", ".join(BUILTIN_OPS)
        return (
            f"invalid caveats: unknown op {json.dumps(op)}: the built-in ops are {builtin}; "
            'a custom op is namespaced, e.g. "tool:search"'
        )
    if any(not _ASCII_PRINTABLE_FIRST <= ord(c) <= _ASCII_PRINTABLE_LAST for c in op):
        return f"invalid caveats: op {json.dumps(op)} contains a non-printable or space character"
    return None


def unlimited_caveats(*ops: Op | str) -> capability_service_pb2.CapabilityCaveats:
    """Caveats granting ``ops`` with no request and no budget limit, the
    budget in the platform's currency: the least Issue takes. To limit one,
    set its ceiling on the result, as ``caveats.max_requests = 100``."""
    return capability_service_pb2.CapabilityCaveats(
        ops=[str(op) for op in ops],
        unlimited_requests=True,
        unlimited_budget=types_pb2.UnlimitedBudget(),
    )


def validate_issue_caveats(caveats: capability_service_pb2.CapabilityCaveats) -> None:
    """Raise ``ValueError`` naming everything Issue would refuse in
    ``caveats`` for their shape: no operation, an operation the server does
    not know, or a request or budget ceiling left unset. It checks nothing
    else: not the resources, where empty ``resource_prefixes`` and
    ``resource_uris`` grant every resource of the tenant rather than none,
    nor what Issue decides by the caller or the tenant. Delegate takes an
    unset ceiling as the parent's, so this is for Issue alone."""
    problems: list[str] = []
    if not caveats.ops:
        problems.append(NO_OPS)
    problems += [f"caveats.ops: {r}" for r in map(op_refusal, caveats.ops) if r is not None]
    if caveats.WhichOneof(_REQUEST_CEILING) is None:
        problems.append(NO_REQUEST_CEILING)
    if caveats.WhichOneof(_BUDGET_CEILING) is None:
        problems.append(NO_BUDGET_CEILING)
    if problems:
        raise ValueError("; ".join(problems))
