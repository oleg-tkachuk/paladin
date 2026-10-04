"""Verifying webhook deliveries.

The server signs each delivery in ``X-Paladin-Webhook-Signature``:
``t=<unix seconds>,v1=<hex HMAC-SHA256 of "<t>.<body>">``, keyed by the
subscription's signing secret. ``verify_webhook`` checks it in constant time
and refuses a delivery whose timestamp is outside the tolerance window, so a
captured delivery cannot be replayed later. The format is pinned by
``sdk/testdata/webhook_signatures.json``, which the server's and the Go SDK's
tests read too.

A replay inside the window verifies by design: deduplicate on the delivery's
``X-Paladin-Event-Id``, which is stable across retries.
"""

from __future__ import annotations

import hashlib
import hmac
import string
import time

HEADER_WEBHOOK_SIGNATURE = "X-Paladin-Webhook-Signature"
# How far a delivery's timestamp may be from the subscriber's clock, either
# way, in seconds.
DEFAULT_WEBHOOK_TOLERANCE = 300.0

_PAIR_SEP = ","
_KEY_VALUE_SEP = "="
_SIGNED_SEP = b"."
_TIME_KEY = "t"
_V1_KEY = "v1"


class WebhookSignatureError(ValueError):
    """A delivery that does not verify: malformed, signed with another
    secret, altered, or outside the tolerance window."""


def _mac(secret: str, t: int, body: bytes) -> bytes:
    return hmac.new(secret.encode(), str(t).encode() + _SIGNED_SEP + body, hashlib.sha256).digest()


def sign_webhook(secret: str, t: int, body: bytes) -> str:
    """The ``X-Paladin-Webhook-Signature`` value for ``body`` signed at unix
    second ``t``, as the server sends it."""
    return f"{_TIME_KEY}{_KEY_VALUE_SEP}{t}{_PAIR_SEP}{_V1_KEY}{_KEY_VALUE_SEP}{_mac(secret, t, body).hex()}"


def verify_webhook(
    secret: str,
    header: str,
    body: bytes,
    *,
    tolerance: float = DEFAULT_WEBHOOK_TOLERANCE,
    now: float | None = None,
) -> None:
    """Check a delivery: ``header`` is its ``X-Paladin-Webhook-Signature``,
    ``body`` its raw bytes as received. Returns when one ``v1`` signature
    matches under ``secret`` and the timestamp is within ``tolerance`` seconds
    of ``now`` (default: the current time); raises ``WebhookSignatureError``
    otherwise."""
    t: int | None = None
    sigs: list[bytes] = []
    for pair in header.split(_PAIR_SEP):
        key, _, value = pair.partition(_KEY_VALUE_SEP)
        if key == _TIME_KEY:
            if t is not None or not value or not all(c in string.digits for c in value):
                raise WebhookSignatureError("bad timestamp")
            t = int(value)
        elif key == _V1_KEY:
            # A value that is not hex cannot match; it is skipped, not fatal,
            # so another v1 beside it can still verify. Checked by hand:
            # bytes.fromhex would also take spaces.
            if value and len(value) % 2 == 0 and all(c in string.hexdigits for c in value):
                sigs.append(bytes.fromhex(value))
    if t is None or not sigs:
        raise WebhookSignatureError("want t=<unix seconds>,v1=<hex>")
    want = _mac(secret, t, body)
    if not any(hmac.compare_digest(sig, want) for sig in sigs):
        raise WebhookSignatureError("no signature matches")
    skew = abs((time.time() if now is None else now) - t)
    if skew > tolerance:
        raise WebhookSignatureError(f"signed {skew:.0f}s from now, beyond {tolerance:.0f}s")
