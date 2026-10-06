"""What the SDK's TLS transports share with the core, which must import
without the ``tls`` extra: the call's deadline and the transport settings."""

from __future__ import annotations

from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any

# The deadline of the RPC being sent, as time.monotonic(); set by the SDK's
# interceptors, which see the call's timeout. pyqwest hands a sync transport
# the timeout only through a private module, so it is carried here instead.
call_deadline: ContextVar[float | None] = ContextVar("paladin_call_deadline", default=None)


@dataclass(frozen=True)
class Settings:
    """The transport settings ``TLS`` honours; pyqwest's names, so that a
    caller's ``Transfer`` settings mean the same over TLS."""

    connect_timeout: float | None = None
    read_timeout: float | None = None
    pool_idle_timeout: float | None = None
    pool_max_idle_per_host: int | None = None
    enable_otel: bool = True
    tracer_provider: Any = None
    # pyqwest follows redirects by default; httpcore never does.
    follow_redirects: bool = field(default=False)
