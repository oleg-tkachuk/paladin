"""The planes a capability names, against sdk/testdata/planes.json, the table
the Go SDK and the server are held to as well."""

from __future__ import annotations

import json
from pathlib import Path

import paladin
from paladin import Plane

SHARED = Path(__file__).resolve().parents[2] / "testdata" / "planes.json"


def test_planes_match_the_shared_table() -> None:
    table = json.loads(SHARED.read_text())["planes"]
    assert {p.value: p.audience for p in Plane} == table


def test_a_plane_is_not_its_audience() -> None:
    for plane in Plane:
        assert plane.value != plane.audience
    assert Plane.DATA.audience == paladin.AUDIENCE_DATA
