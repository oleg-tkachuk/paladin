"""The resource a capability names an object by, against
sdk/testdata/object_resources.json, which the server and the Go SDK read too."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from paladin import object_resource
from paladin.testing import _match_resource  # pyright: ignore[reportPrivateUsage]

TABLE = Path(__file__).resolve().parents[2] / "testdata" / "object_resources.json"
TENANT_PLACEHOLDER = "{tenant}"

_TABLE: dict[str, Any] = json.loads(TABLE.read_text())
_TENANT: str = _TABLE["tenant"]


@pytest.mark.parametrize("case", _TABLE["resources"], ids=lambda c: f"{c['collection']}|{c['key']}")
def test_object_resource_is_the_tables(case: dict[str, str]) -> None:
    want = case["uri"].replace(TENANT_PLACEHOLDER, _TENANT)
    assert object_resource(_TENANT, case["collection"], case["key"]) == want


def test_no_two_objects_share_a_resource() -> None:
    uris = [object_resource(_TENANT, c["collection"], c["key"]) for c in _TABLE["resources"]]
    assert len(uris) == len(set(uris))


@pytest.mark.parametrize("case", _TABLE["covers"], ids=lambda c: c["name"])
def test_a_grant_covers_only_its_own_collection(case: dict[str, Any]) -> None:
    grant = object_resource(_TENANT, case["grant"]["collection"], case["grant"]["key"])
    obj = object_resource(_TENANT, case["object"]["collection"], case["object"]["key"])
    assert _match_resource(grant, obj) is case["match"]
