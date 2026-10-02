"""README.md's method tables must list every generated service and method."""

from __future__ import annotations

import importlib
import inspect
import pkgutil
import re
from pathlib import Path

README = Path(__file__).resolve().parents[1] / "README.md"
PLANES = ("admin", "data", "iam")
CLIENT_SUFFIX = "ClientSync"
CONNECT_MODULE_SUFFIX = "_connect"
# The lines git writes into a file it could not merge, diff3's base marker
# included: seven of the same character at line start.
CONFLICT_MARKER = re.compile(r"^(<{7}|\|{7}|={7}|>{7})( |$)", re.MULTILINE)


def _rows() -> dict[str, str]:
    rows = {}
    for line in README.read_text().splitlines():
        cells = line.split("|")
        if len(cells) >= 4:
            rows[cells[1].strip().strip("`")] = cells[2]
    return rows


def _generated() -> dict[str, list[str]]:
    services: dict[str, list[str]] = {}
    for plane in PLANES:
        package = importlib.import_module(f"paladin.{plane}.v1")
        for info in pkgutil.iter_modules(package.__path__):
            if not info.name.endswith(CONNECT_MODULE_SUFFIX):
                continue
            module = importlib.import_module(f"{package.__name__}.{info.name}")
            for name, cls in vars(module).items():
                # Defined here, not imported: the base class also ends in ClientSync.
                if not (inspect.isclass(cls) and name.endswith(CLIENT_SUFFIX)):
                    continue
                if cls.__module__ != module.__name__:
                    continue
                services[name.removesuffix(CLIENT_SUFFIX)] = [
                    attr
                    for attr, value in vars(cls).items()
                    if not attr.startswith("_") and callable(value)
                ]
    return services


def test_readme_lists_every_method() -> None:
    rows = _rows()
    services = _generated()
    assert services, "no generated services found — this test is asserting nothing"
    missing = []
    for svc, methods in services.items():
        if svc not in rows:
            missing.append(svc)
            continue
        missing += [f"{svc}.{m}" for m in methods if f"`{m}`" not in rows[svc]]
    assert not missing, f"README.md does not list: {', '.join(missing)}"


def test_readme_has_no_conflict_markers() -> None:
    # A release tag publishes the README as it is.
    found = CONFLICT_MARKER.findall(README.read_text())
    assert not found, f"README.md holds merge-conflict markers: {found}"
