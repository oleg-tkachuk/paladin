"""The NUL walker, against sdk/testdata/request_nul.json — the cases the
server's walker and the Go SDK's are tested with too, so the three refuse the
same requests, naming the same path."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from google.protobuf import json_format, symbol_database, wrappers_pb2

from paladin import _protonul

# Registers the request messages the shared table names.
from paladin.data.v1 import object_service_pb2, operation_service_pb2  # noqa: F401
from paladin.iam.v1 import user_settings_service_pb2  # noqa: F401

SHARED = Path(__file__).resolve().parents[2] / "testdata" / "request_nul.json"
WITH_NUL = "a\x00b"
"""Request text holding NUL between printable characters."""


def _table() -> dict[str, Any]:
    return json.loads(SHARED.read_text())


def test_the_refusal_suffix_is_the_shared_one() -> None:
    assert _protonul.REFUSAL_SUFFIX == _table()["refusal_suffix"]
    assert _protonul.refusal("key") == "key" + _table()["refusal_suffix"]


@pytest.mark.parametrize("case", _table()["cases"], ids=lambda c: c["name"])
def test_find_agrees_with_the_shared_table(case: dict[str, Any]) -> None:
    message = symbol_database.Default().GetSymbol(case["message"])()
    json_format.ParseDict(case["request"], message)
    assert _protonul.find(message) == (case["path"] or None)


@pytest.mark.parametrize(
    ("message", "path"),
    [
        (wrappers_pb2.StringValue(value=WITH_NUL), "value"),
        (wrappers_pb2.BytesValue(value=WITH_NUL.encode()), None),
    ],
    ids=["a string wrapper", "bytes are skipped"],
)
def test_find_wrappers(message: Any, path: str | None) -> None:
    """What the shared table cannot carry in protobuf JSON: a wrapper at the
    top, and a bytes field, which is not text."""
    assert _protonul.find(message) == path
