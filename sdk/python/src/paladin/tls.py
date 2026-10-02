"""Connections over TLS with files that rotate on disk.

``TLS`` builds the transports for ``connect(tls=…)`` (the RPCs) and
``Transfer(tls=…)`` (the presigned requests): a CA bundle, a client
certificate and an expected server identity, re-read whenever the files
change, so certificates a workload-identity agent rotates are picked up
without a restart. It matches the Go SDK's ``TLS`` field for field — the same
defaults, the same checks, errors of the same names.

The connections are made by the standard library's ``ssl`` under httpcore
(``paladin._tls_http`` says why, and why the check is safe); plaintext ones
stay on pyqwest.
"""

from __future__ import annotations

import os
import re
import ssl
import threading
import time
import warnings
from collections.abc import Callable
from dataclasses import fields
from pathlib import Path
from typing import Any

import pyqwest
from cryptography import x509
from cryptography.x509.oid import ExtensionOID

from paladin._tls_http import AsyncTLSTransport, Settings, SyncTLSTransport

DEFAULT_TLS_RELOAD_INTERVAL = 30.0
"""Seconds between checks of the TLS files for a change."""
DEFAULT_TLS_MIN_VERSION = ssl.TLSVersion.TLSv1_2
"""The lowest TLS version offered when ``min_version`` is not given, and the
lowest it may be."""
_ALLOWED_MIN_VERSIONS = (ssl.TLSVersion.TLSv1_2, ssl.TLSVersion.TLSv1_3)

# A SPIFFE ID, spiffe://trust-domain[/path], as the SPIFFE ID specification
# and go-spiffe's spiffeid.FromString define it: a lower-case trust domain of
# [a-z0-9._-], path segments of [a-zA-Z0-9._-], no empty segment, no "." or
# "..", no trailing slash, no query, fragment, port or user info.
# sdk/testdata/spiffe_ids.json holds the cases both SDKs must agree on.
#
# py-spiffe, the SPIFFE project's Python library, is not used: it depends on
# grpcio for its Workload API, a native dependency the SDK has no other use
# for. What is needed from it here is this grammar and the X.509-SVID leaf
# rules below, both of which the specification fixes.
_SPIFFE_ID = re.compile(r"spiffe://[a-z0-9._-]+(?:/[A-Za-z0-9._-]+)*")
_DOT_SEGMENTS = frozenset({".", ".."})
_SPIFFE_SCHEME = "spiffe://"


class TLSKeyPairError(ValueError):
    """``cert_file`` without ``key_file``, or the reverse: Go's ErrTLSKeyPair."""


class NoCAError(ValueError):
    """The CA file holds no PEM certificate: Go's ErrNoCA."""


class ServerIDError(ValueError):
    """The server's certificate is not the expected SPIFFE ID, or the ID
    given is not one: Go's ErrServerID. Through ``connect`` it is the
    ``__cause__`` of the ``ConnectError`` the call raises."""


class ServerIDNeedsCAError(ValueError):
    """``server_id`` without ``ca_file``: a SPIFFE ID is checked against its
    trust domain's bundle, which the system roots are not. Go's
    ErrServerIDNeedsCA."""


class TLSMinVersionError(ValueError):
    """A ``min_version`` below ``DEFAULT_TLS_MIN_VERSION``, or not a TLS
    version: Go's ErrTLSMinVersion."""


class TLSAndHTTPError(ValueError):
    """``tls`` together with an HTTP client or transport of your own, which
    ``tls`` would replace: Go's ErrTLSAndHTTP. Wrap ``TLS.sync_transport()``
    instead."""


def parse_spiffe_id(value: str) -> str:
    """``value`` if it is a SPIFFE ID; ``ServerIDError`` if not."""
    if not _SPIFFE_ID.fullmatch(value):
        raise ServerIDError(f"paladin: {value!r} is not a SPIFFE ID")
    path = value[len(_SPIFFE_SCHEME) :].split("/")[1:]
    if any(segment in _DOT_SEGMENTS for segment in path):
        raise ServerIDError(f"paladin: {value!r} is not a SPIFFE ID: a dot segment")
    return value


def _verify_svid(leaf: x509.Certificate, server_id: str) -> None:
    """The X.509-SVID rules for a leaf, then its ID against ``server_id``.
    The chain was verified against the trust bundle by OpenSSL already."""
    try:
        sans = leaf.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_ALTERNATIVE_NAME)
        uris = sans.value.get_values_for_type(x509.UniformResourceIdentifier)
    except x509.ExtensionNotFound:
        uris = []
    if len(uris) != 1:
        raise ssl.SSLCertVerificationError(
            f"paladin: an X.509-SVID has exactly one URI SAN; the server's has {len(uris)}"
        )
    try:
        constraints = leaf.extensions.get_extension_for_oid(ExtensionOID.BASIC_CONSTRAINTS)
        if constraints.value.ca:
            raise ssl.SSLCertVerificationError("paladin: the server's SVID is a CA certificate")
    except x509.ExtensionNotFound:
        pass
    try:
        usage = leaf.extensions.get_extension_for_oid(ExtensionOID.KEY_USAGE).value
    except x509.ExtensionNotFound as err:
        raise ssl.SSLCertVerificationError("paladin: the server's SVID has no key usage") from err
    if not usage.digital_signature or usage.key_cert_sign or usage.crl_sign:
        raise ssl.SSLCertVerificationError(
            "paladin: the server's SVID key usage is not digitalSignature alone"
        )
    if uris[0] != server_id:
        raise ServerIDError(
            f"paladin: the server certificate is {uris[0]!r}, not the expected {server_id!r}"
        )


class TLS:
    """A CA bundle, an optional client certificate for mutual TLS, and an
    optional server identity.

    ``ca_file`` is a PEM bundle the server's chain must reach; without it the
    system roots are trusted. ``cert_file`` and ``key_file`` are the client
    certificate — both or neither. ``server_id``, when given, is the SPIFFE
    ID the server must present: its certificate is verified as an X.509-SVID
    against ``ca_file`` as the trust bundle, instead of against the host
    name, which an SVID does not carry. ``verify_peer`` runs after those
    checks on the server's leaf certificate (a ``cryptography`` certificate);
    an exception from it refuses the connection. ``min_version`` is the
    lowest TLS version offered.

    The files are read when a transport is built, so a missing one fails
    there, and again when their modification times change, checked at most
    every ``reload_interval`` seconds. A rotation caught half-written — a new
    certificate beside the old key — keeps the last good files until the
    next check. After a rotation every new request goes out on a connection
    made with the new files; a request in flight finishes on its own, and
    the old connections close once nothing uses them.
    """

    def __init__(
        self,
        *,
        ca_file: str | os.PathLike[str] | None = None,
        cert_file: str | os.PathLike[str] | None = None,
        key_file: str | os.PathLike[str] | None = None,
        reload_interval: float = DEFAULT_TLS_RELOAD_INTERVAL,
        server_id: str | None = None,
        verify_peer: Callable[[x509.Certificate], None] | None = None,
        min_version: ssl.TLSVersion = DEFAULT_TLS_MIN_VERSION,
    ) -> None:
        if (cert_file is None) != (key_file is None):
            raise TLSKeyPairError("a client certificate needs both cert_file and key_file")
        if min_version not in _ALLOWED_MIN_VERSIONS:
            raise TLSMinVersionError(f"paladin: TLS min_version {min_version!r} is below TLS 1.2")
        if server_id is not None:
            parse_spiffe_id(server_id)
            if ca_file is None:
                raise ServerIDNeedsCAError("paladin: server_id needs ca_file, the trust bundle")
        self.ca_file = Path(ca_file) if ca_file is not None else None
        self.cert_file = Path(cert_file) if cert_file is not None else None
        self.key_file = Path(key_file) if key_file is not None else None
        self.reload_interval = reload_interval
        self.server_id = server_id
        self.verify_peer = verify_peer
        self.min_version = min_version

    def _paths(self) -> list[Path]:
        return [p for p in (self.ca_file, self.cert_file, self.key_file) if p is not None]

    def _custom(self) -> bool:
        """Whether this asks for what only the SDK's own transports do."""
        return (
            self.server_id is not None
            or self.verify_peer is not None
            or self.min_version != DEFAULT_TLS_MIN_VERSION
        )

    def sync_transport(self, **settings: Any) -> Any:
        """A ``pyqwest.SyncTransport`` with these files — the Go SDK's
        ``TLS.RoundTripper()`` — for a client of your own, which may wrap it.
        ``settings`` are pyqwest's transport settings the SDK honours:
        ``connect_timeout``, ``read_timeout``, ``pool_idle_timeout``,
        ``pool_max_idle_per_host``, ``enable_otel``, ``tracer_provider`` and
        ``follow_redirects=False``."""
        legacy = _legacy(self, settings)
        if legacy is not None:
            return _RotatingPyqwest(self, pyqwest.SyncHTTPTransport, legacy)
        return SyncTLSTransport(_Files(self), _settings(settings))

    def async_transport(self, **settings: Any) -> Any:
        """``sync_transport`` for the async clients."""
        legacy = _legacy(self, settings)
        if legacy is not None:
            return _RotatingPyqwest(self, pyqwest.HTTPTransport, legacy)
        return AsyncTLSTransport(_Files(self), _settings(settings))


_SETTINGS = frozenset(f.name for f in fields(Settings))


def _settings(given: dict[str, Any]) -> Settings:
    if given.get("follow_redirects"):
        raise ValueError("paladin: TLS transports do not follow redirects")
    return Settings(**given)


def _legacy(tls: TLS, given: dict[str, Any]) -> dict[str, Any] | None:
    """The settings, when some are pyqwest's that the SDK's transports do not
    take: those keep the pyqwest transport, for one more release."""
    unknown = sorted(set(given) - _SETTINGS)
    if not unknown:
        return None
    if tls._custom():
        raise TypeError(
            f"paladin: TLS transports do not take {', '.join(unknown)}; "
            "server_id, verify_peer and min_version need the SDK's own"
        )
    warnings.warn(
        f"paladin: TLS transport settings {', '.join(unknown)} are deprecated and will be "
        "refused in the next release; such a transport keeps pyqwest, without "
        "closing connections a rotation replaced",
        DeprecationWarning,
        stacklevel=3,
    )
    return given


class _Files:
    """What the files held when last read, as an ``ssl`` context, re-read when
    their modification times change."""

    def __init__(self, tls: TLS) -> None:
        self._tls = tls
        self._lock = threading.Lock()
        self._mtimes, self._context = self._load()
        self._checked = time.monotonic()

    def _load(self) -> tuple[tuple[int, ...], ssl.SSLContext]:
        tls = self._tls
        mtimes = tuple(p.stat().st_mtime_ns for p in tls._paths())
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)  # verifies chain and host
        context.minimum_version = tls.min_version
        if tls.ca_file is not None:
            pem = tls.ca_file.read_bytes()
            try:
                x509.load_pem_x509_certificates(pem)
            except ValueError as err:
                raise NoCAError(f"paladin: {tls.ca_file} holds no PEM certificate") from err
            context.load_verify_locations(cadata=pem.decode("ascii"))
        else:
            context.load_default_certs()
        if tls.server_id is not None:
            # An SVID names a workload, not a host: the ID is checked instead.
            context.check_hostname = False
        if tls.cert_file is not None and tls.key_file is not None:
            context.load_cert_chain(tls.cert_file, tls.key_file)
        return mtimes, context

    def current(self) -> tuple[tuple[int, ...], ssl.SSLContext]:
        """The files' key and context, re-read once the interval has passed
        and one changed. A rotation caught half-written keeps the last good
        context; the next check tries again."""
        with self._lock:
            now = time.monotonic()
            if now - self._checked >= self._tls.reload_interval:
                self._checked = now
                try:
                    if tuple(p.stat().st_mtime_ns for p in self._tls._paths()) != self._mtimes:
                        self._mtimes, self._context = self._load()
                except (OSError, ValueError, ssl.SSLError):
                    pass
            return self._mtimes, self._context

    def verify(self, peer: Any) -> None:
        """The checks after the handshake: the SVID, then ``verify_peer``."""
        tls = self._tls
        if tls.server_id is None and tls.verify_peer is None:
            return
        # Positional: httpcore hands over ssl's C object, whose getpeercert
        # takes no keyword.
        der = peer.getpeercert(True)
        if der is None:
            raise ssl.SSLCertVerificationError("paladin: the server sent no certificate")
        leaf = x509.load_der_x509_certificate(der)
        if tls.server_id is not None:
            _verify_svid(leaf, tls.server_id)
        if tls.verify_peer is not None:
            tls.verify_peer(leaf)


class _RotatingPyqwest:
    """The pyqwest transport for settings only pyqwest takes, rebuilt when the
    files change; deprecated with them."""

    def __init__(self, tls: TLS, build: Any, settings: dict[str, Any]) -> None:
        self._tls, self._build, self._settings = tls, build, settings
        self._lock = threading.Lock()
        self._mtimes, self._current = self._make()
        self._checked = time.monotonic()

    def _make(self) -> tuple[tuple[int, ...], Any]:
        tls = self._tls
        mtimes = tuple(p.stat().st_mtime_ns for p in tls._paths())
        args: dict[str, Any] = {"tls_include_system_certs": tls.ca_file is None}
        if tls.ca_file is not None:
            args["tls_ca_cert"] = tls.ca_file.read_bytes()
        if tls.cert_file is not None and tls.key_file is not None:
            args["tls_cert"] = tls.cert_file.read_bytes()
            args["tls_key"] = tls.key_file.read_bytes()
        return mtimes, self._build(**args, **self._settings)

    def _inner(self) -> Any:
        with self._lock:
            now = time.monotonic()
            if now - self._checked >= self._tls.reload_interval:
                self._checked = now
                try:
                    if tuple(p.stat().st_mtime_ns for p in self._tls._paths()) != self._mtimes:
                        self._mtimes, self._current = self._make()
                except (OSError, ValueError, RuntimeError):
                    pass
            return self._current

    def execute_sync(self, request: Any) -> Any:
        return self._inner().execute_sync(request)

    async def execute(self, request: Any) -> Any:
        return await self._inner().execute(request)
