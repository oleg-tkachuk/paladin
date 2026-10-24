"""What the SDK tells a caller to install when an extra is missing: one
command, the project's package manager's, naming an extra that exists."""

from __future__ import annotations

import sys
import tomllib
from pathlib import Path

import pytest

from paladin import biscuit, dpop, testing, tls

PYPROJECT = Path(__file__).resolve().parents[1] / "pyproject.toml"
DISTRIBUTION = "paladin-sdk"

HINTS = {
    "tls": tls.EXTRA_HINT,
    "dpop": dpop.EXTRA_HINT,
    "biscuit": biscuit.EXTRA_HINT,
    "testing": testing.TESTING_EXTRA_HINT,
}


@pytest.mark.parametrize(("extra", "hint"), HINTS.items(), ids=HINTS.keys())
def test_a_hint_names_an_extra_the_project_declares(extra: str, hint: str) -> None:
    extras = tomllib.loads(PYPROJECT.read_text())["project"]["optional-dependencies"]
    assert extra in extras
    assert hint.endswith(f"uv add '{DISTRIBUTION}[{extra}]'")


@pytest.mark.parametrize(
    ("module", "load", "hint"),
    [
        ("cryptography.hazmat.primitives", dpop._crypto, dpop.EXTRA_HINT),
        ("biscuit_auth", biscuit._biscuit, biscuit.EXTRA_HINT),
    ],
    ids=["dpop", "biscuit"],
)
def test_without_the_extra_the_hint_is_raised(
    monkeypatch: pytest.MonkeyPatch, module: str, load: object, hint: str
) -> None:
    # A None entry makes the import fail as an absent package does.
    monkeypatch.setitem(sys.modules, module, None)
    with pytest.raises(ImportError) as err:
        load()  # type: ignore[operator]
    assert str(err.value) == hint
