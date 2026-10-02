"""The cookbook in examples/ runs: each recipe against paladin.testing, or —
for the ones that only configure a client — builds it."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path
from types import ModuleType

import pytest

from paladin.testing import FakePaladin

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def load(name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(f"examples.{name}", EXAMPLES / f"{name}.py")
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def fake():  # type: ignore[no-untyped-def]
    with FakePaladin() as f:
        yield f


def test_every_example_is_tested() -> None:
    tested = {
        "split_horizon",
        "mtls",
        "rotating_token",
        "bulk_ingestion",
        "resumable_multipart",
        "streaming",
        "migrating_from_connect_json",
    }
    assert {p.stem for p in EXAMPLES.glob("*.py")} == tested


def test_split_horizon() -> None:
    assert load("split_horizon").build().data is not None


def test_mtls_reads_its_files_at_start_up() -> None:
    with pytest.raises(FileNotFoundError):
        load("mtls").build()


def test_rotating_token() -> None:
    m = load("rotating_token")
    store = m.SettingsStore("first")
    source = m.StoreToken(store)
    store.rotate("second")
    assert source.token("paladin-data") == "second"
    assert m.build(store).data is not None


def test_bulk_ingestion(fake: FakePaladin) -> None:
    total, failed, async_total = load("bulk_ingestion").main(fake)
    assert (total, len(failed), async_total) == (15, 1, 15)


def test_resumable_multipart(fake: FakePaladin) -> None:
    assert load("resumable_multipart").main(fake) == (1, 3, True)


def test_streaming(fake: FakePaladin) -> None:
    assert load("streaming").main(fake)


def test_migrating_from_connect_json(fake: FakePaladin) -> None:
    assert load("migrating_from_connect_json").main(fake)
