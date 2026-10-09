"""Make a resource exist, for provisioning that runs at every boot."""

from __future__ import annotations

from collections.abc import Awaitable, Callable

from connectrpc.code import Code
from connectrpc.errors import ConnectError


def ensure[T](get: Callable[[], T], create: Callable[[], T]) -> tuple[T, bool]:
    """Call ``get``; when it raises NotFound, ``create``; when the create
    raises AlreadyExists — another process got there first — ``get`` again.
    Returns the resource and whether this call created it. Any other error
    from either call is raised as it is.

    Run ``create`` under a stable ``idempotency_key``, derived from what is
    being created, so a create retried after a lost response is answered from
    the first rather than meeting AlreadyExists."""
    try:
        return get(), False
    except ConnectError as err:
        if err.code != Code.NOT_FOUND:
            raise
    try:
        return create(), True
    except ConnectError as err:
        if err.code != Code.ALREADY_EXISTS:
            raise
    return get(), False


async def aensure[T](
    get: Callable[[], Awaitable[T]], create: Callable[[], Awaitable[T]]
) -> tuple[T, bool]:
    """``ensure`` for the async clients."""
    try:
        return await get(), False
    except ConnectError as err:
        if err.code != Code.NOT_FOUND:
            raise
    try:
        return await create(), True
    except ConnectError as err:
        if err.code != Code.ALREADY_EXISTS:
            raise
    return await get(), False
