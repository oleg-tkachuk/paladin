"""The tls extra: TLS's HTTP stack is installed and imported only for it."""

from __future__ import annotations

import subprocess
import sys

import pytest

import paladin
from paladin import tls

# Modules only the tls and dpop extras install; importing paladin must not
# load them.
_EXTRA_MODULES = ("httpcore", "h2", "anyio", "cryptography", "paladin._tls_http")


def test_importing_the_sdk_loads_no_tls_stack() -> None:
    probe = (
        f"import sys, paladin; print(','.join(m for m in {_EXTRA_MODULES!r} if m in sys.modules))"
    )
    loaded = subprocess.run(
        [sys.executable, "-c", probe], check=True, capture_output=True, text=True
    ).stdout.strip()
    assert loaded == "", f"import paladin loaded {loaded}"


def _without_the_extra(monkeypatch: pytest.MonkeyPatch) -> None:
    """Imports of the TLS stack fail as they do where it is not installed."""
    # A None entry makes the import fail as an absent package does; the
    # attribute an earlier import left on the package would answer instead.
    monkeypatch.setitem(sys.modules, "paladin._tls_http", None)
    monkeypatch.delattr(paladin, "_tls_http", raising=False)


def test_tls_without_the_extra_says_what_to_install(monkeypatch: pytest.MonkeyPatch) -> None:
    _without_the_extra(monkeypatch)
    with pytest.raises(ImportError) as err:
        paladin.TLS()
    assert str(err.value) == tls.EXTRA_HINT


def test_plaintext_clients_need_no_extra(monkeypatch: pytest.MonkeyPatch) -> None:
    _without_the_extra(monkeypatch)
    p = paladin.connect(paladin.Endpoints(data="http://127.0.0.1:1"))
    assert p.data is not None
