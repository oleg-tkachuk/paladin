"""The data plane's capability operations, against sdk/testdata/capability_ops.json,
the table the Go SDK, the server and the console are held to as well."""

from __future__ import annotations

import json
from pathlib import Path

from paladin import (
    OP_DELETE,
    OP_GET,
    OP_LIST,
    OP_MANAGE,
    OP_OBJECT_PRESIGN,
    OP_OBJECT_TAG,
    OP_PUT,
    OP_SHARE,
)

SHARED = Path(__file__).resolve().parents[2] / "testdata" / "capability_ops.json"


def test_ops_match_the_shared_table() -> None:
    table = json.loads(SHARED.read_text())
    assert table["data_plane"] == {"object_presign": OP_OBJECT_PRESIGN, "object_tag": OP_OBJECT_TAG}


def test_shared_ops_match_the_table() -> None:
    table = json.loads(SHARED.read_text())
    assert table["limes"] == [OP_GET, OP_PUT, OP_LIST, OP_DELETE, OP_SHARE, OP_MANAGE]
