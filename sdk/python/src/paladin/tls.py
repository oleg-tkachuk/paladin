"""Connections over TLS with files that rotate on disk.

``TLS`` builds the ``pyqwest`` transports for ``connect(tls=…)`` (the RPCs)
and ``Transfer(tls=…)`` (the presigned requests): a CA bundle and a client
certificate, re-read whenever they change, so certificates a workload-identity
agent rotates are picked up without a restart.

Unlike the Go SDK, there is no check of the server's SPIFFE ID or other URI
SAN: the HTTP stack connect-python runs on (``pyqwest``, over Rust's
``reqwest``) offers no hook into peer verification. The server's certificate
is verified against the CA bundle and the host name, so it must name the host
it is reached at.
"""

from __future__ import annotations

import os
import threading
import time
from pathlib import Path
from typing import Any

import pyqwest

DEFAULT_TLS_RELOAD_INTERVAL = 30.0
"""Seconds between checks of the TLS files for a change."""


class TLS:
    """A CA bundle and an optional client certificate for mutual TLS.

    ``ca_file`` is a PEM bundle the server's chain must reach; without it the
    system roots are trusted. ``cert_file`` and ``key_file`` are the client
    certificate — both or neither. The files are read when a transport is
    built, so a missing one fails there, and again when their modification
    times change, checked at most every ``reload_interval`` seconds. A
    rotation caught half-written — a new certificate beside the old key —
    keeps the last good files until the next check.
    """

    def __init__(
        self,
        *,
        ca_file: str | os.PathLike[str] | None = None,
        cert_file: str | os.PathLike[str] | None = None,
        key_file: str | os.PathLike[str] | None = None,
        reload_interval: float = DEFAULT_TLS_RELOAD_INTERVAL,
    ) -> None:
        if (cert_file is None) != (key_file is None):
            raise ValueError("a client certificate needs both cert_file and key_file")
        self.ca_file = Path(ca_file) if ca_file is not None else None
        self.cert_file = Path(cert_file) if cert_file is not None else None
        self.key_file = Path(key_file) if key_file is not None else None
        self.reload_interval = reload_interval

    def _paths(self) -> list[Path]:
        return [p for p in (self.ca_file, self.cert_file, self.key_file) if p is not None]

    def _read(self) -> tuple[dict[str, Any], tuple[int, ...]]:
        """The transport's TLS arguments from the files, and their mtimes."""
        mtimes = tuple(p.stat().st_mtime_ns for p in self._paths())
        args: dict[str, Any] = {"tls_include_system_certs": self.ca_file is None}
        if self.ca_file is not None:
            args["tls_ca_cert"] = self.ca_file.read_bytes()
        if self.cert_file is not None and self.key_file is not None:
            args["tls_cert"] = self.cert_file.read_bytes()
            args["tls_key"] = self.key_file.read_bytes()
        return args, mtimes

    def sync_transport(self, **transport: Any) -> RotatingSyncTransport:
        """A ``pyqwest.SyncTransport`` with these files; ``transport`` is
        passed on to each ``SyncHTTPTransport`` it builds (timeouts, pool)."""
        return RotatingSyncTransport(self, pyqwest.SyncHTTPTransport, transport)

    def async_transport(self, **transport: Any) -> RotatingTransport:
        """``sync_transport`` for the async clients."""
        return RotatingTransport(self, pyqwest.HTTPTransport, transport)


class _Rotating:
    """Holds the transport built from the files as they were last read, and
    builds a new one when they change. Calls already in flight finish on the
    transport they started on."""

    def __init__(self, tls: TLS, build: Any, transport: dict[str, Any]) -> None:
        self._tls, self._build, self._transport = tls, build, transport
        self._lock = threading.Lock()
        args, self._mtimes = tls._read()
        self._current = build(**args, **transport)
        self._checked = time.monotonic()

    def _inner(self) -> Any:
        with self._lock:
            now = time.monotonic()
            if now - self._checked < self._tls.reload_interval:
                return self._current
            self._checked = now
            try:
                mtimes = tuple(p.stat().st_mtime_ns for p in self._tls._paths())
                if mtimes != self._mtimes:
                    args, mtimes = self._tls._read()
                    self._current = self._build(**args, **self._transport)
                    self._mtimes = mtimes
            except (OSError, ValueError, RuntimeError):
                # Mid-rotation: keep the last good transport; the next check
                # tries again.
                pass
            return self._current


class RotatingSyncTransport(_Rotating):
    """A ``pyqwest.SyncTransport`` over files that rotate; see ``TLS``."""

    def execute_sync(self, request: Any) -> Any:
        return self._inner().execute_sync(request)


class RotatingTransport(_Rotating):
    """A ``pyqwest.Transport`` over files that rotate; see ``TLS``."""

    async def execute(self, request: Any) -> Any:
        return await self._inner().execute(request)
