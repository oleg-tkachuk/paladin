"""Hooks and logging for retries and presigned transfers.

Nothing is observed unless asked. Records go to the standard ``logging``
logger ``paladin`` at debug level, failed transfers at warning, with their
fields in ``extra`` for a structured formatter; hooks are for metrics of
your own. Tracing goes through connectrpc-otel and pyqwest — see the
README's OpenTelemetry section.
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass

LOGGER_NAME = "paladin"
"""The ``logging`` logger the SDK's records go to."""
logger = logging.getLogger(LOGGER_NAME)


@dataclass(frozen=True)
class RetryEvent:
    """A retry about to be made: the failed ``attempt`` (from 1), the
    ``wait`` before the next one in seconds, and the ``error``."""

    procedure: str
    attempt: int
    wait: float
    error: BaseException


@dataclass(frozen=True)
class TransferEvent:
    """A presigned request that ended: ``host`` it went to (the URL's query
    is the signature and is not kept), ``bytes`` moved, ``duration`` in
    seconds, and ``error`` — None for a success."""

    method: str
    host: str
    bytes: int
    duration: float
    error: BaseException | None = None


@dataclass(frozen=True)
class Hooks:
    """Called as calls are retried and bytes move. Each is optional; they run
    on the calling thread and must not block."""

    on_retry: Callable[[RetryEvent], None] | None = None
    on_transfer: Callable[[TransferEvent], None] | None = None


def report_retry(hooks: Hooks | None, event: RetryEvent) -> None:
    if hooks is not None and hooks.on_retry is not None:
        hooks.on_retry(event)
    logger.debug(
        "paladin: retrying %s after attempt %d",
        event.procedure,
        event.attempt,
        extra={
            "procedure": event.procedure,
            "attempt": event.attempt,
            "wait": event.wait,
            "error": repr(event.error),
        },
    )


def report_transfer(hooks: Hooks | None, event: TransferEvent) -> None:
    if hooks is not None and hooks.on_transfer is not None:
        hooks.on_transfer(event)
    fields = {
        "method": event.method,
        "host": event.host,
        "bytes": event.bytes,
        "duration": event.duration,
    }
    if event.error is not None:
        logger.warning(
            "paladin: transfer %s %s failed",
            event.method,
            event.host,
            extra={**fields, "error": repr(event.error)},
        )
        return
    logger.debug("paladin: transfer %s %s", event.method, event.host, extra=fields)
