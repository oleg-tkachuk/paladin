"""Retries for presigned transfers: a failed request is sent again through a
freshly presigned URL, so an attempt never reuses one that expired or was
refused; a request that cannot succeed by repeating — a 4xx other than an
expiry — is not retried."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from typing import Any

import pyqwest

from paladin.transfer import TransferError

PRESIGN_EXPIRY_SKEW = 30.0
"""Seconds before its expiry a URL is presigned again instead of sent: room
for clock skew and for the request itself."""
_BACKOFF_BASE = 0.2
_BACKOFF_MAX = 5.0
_STATUS_FORBIDDEN = 403
_STATUS_PRECONDITION_FAILED = 412
# Statuses a presigned request may be retried after: the store was busy or
# down, not refusing this request.
_RETRYABLE_STATUS = frozenset({408, 429, 500, 502, 503, 504})
# In what S3-compatible stores answer when a presigned URL, or the session
# credentials that signed it, has expired ("Request has expired",
# "ExpiredToken").
_EXPIRED_MARKER = "expired"
# Transport failures: the connection broke, not the request was refused.
_TRANSPORT_ERRORS = (
    OSError,
    pyqwest.ReadError,
    pyqwest.WriteError,
    pyqwest.RemoteProtocolError,
)


def expired(err: BaseException) -> bool:
    """Whether ``err`` is storage refusing a presigned URL because it expired."""
    return (
        isinstance(err, TransferError)
        and err.status == _STATUS_FORBIDDEN
        and _EXPIRED_MARKER in err.body.lower()
    )


def already_stored(err: BaseException) -> bool:
    """Whether ``err`` is storage refusing an upload because an object is
    already at the key — the URL's If-None-Match held. After a PUT whose
    answer was lost, that means the first attempt landed."""
    return isinstance(err, TransferError) and err.status == _STATUS_PRECONDITION_FAILED


def retryable(err: BaseException) -> bool:
    """Whether sending again, through a fresh URL, may succeed: a busy or
    failing store, an expired URL, or a transport failure."""
    if isinstance(err, TransferError):
        return err.status in _RETRYABLE_STATUS or expired(err)
    return isinstance(err, _TRANSPORT_ERRORS)


def presign_expiry(signed: Any) -> datetime | None:
    """When a presigned URL stops working, from its ``expires_at_rfc3339``, as
    an aware UTC ``datetime``; None when the server sent none or one that does
    not parse. Treat it as an upper bound: the signer may clamp a TTL further."""
    raw = getattr(signed, "expires_at_rfc3339", "")
    if not raw:
        return None
    try:
        exp = datetime.fromisoformat(raw)
    except ValueError:
        return None
    return exp if exp.tzinfo is not None else exp.replace(tzinfo=UTC)


def usable(signed: Any, now: float | None = None) -> bool:
    """Whether a presigned URL still has ``PRESIGN_EXPIRY_SKEW`` left; one with
    no expiry, or an unreadable one, is taken as usable."""
    exp = presign_expiry(signed)
    if exp is None:
        return True
    current = datetime.now(UTC).timestamp() if now is None else now
    return exp.timestamp() - current > PRESIGN_EXPIRY_SKEW


def backoff(attempt: int) -> float:
    """The wait before attempt ``attempt`` + 1: base, doubling, capped."""
    return min(_BACKOFF_BASE * (2 ** (attempt - 1)), _BACKOFF_MAX)


def with_retries[T](
    attempts: int,
    first: Any,
    presign: Callable[[], Any],
    send: Callable[[Any], T],
) -> T:
    """Send through ``first`` — presigning again when it is near its expiry —
    and on a retryable failure wait, presign a fresh URL and send again, up to
    ``attempts`` times. ``send`` must be safe to repeat."""
    signed = first
    attempt = 1
    while True:
        if signed is None or not usable(signed):
            signed = presign()
        try:
            return send(signed)
        except Exception as err:
            if attempt >= attempts or not retryable(err):
                raise
        time.sleep(backoff(attempt))
        attempt += 1
        signed = None


async def awith_retries[T](
    attempts: int,
    first: Any,
    presign: Callable[[], Awaitable[Any]],
    send: Callable[[Any], Awaitable[T]],
) -> T:
    """``with_retries`` for the async workflows."""
    signed = first
    attempt = 1
    while True:
        if signed is None or not usable(signed):
            signed = await presign()
        try:
            return await send(signed)
        except Exception as err:
            if attempt >= attempts or not retryable(err):
                raise
        await asyncio.sleep(backoff(attempt))
        attempt += 1
        signed = None
