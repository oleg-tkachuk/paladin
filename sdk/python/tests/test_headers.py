"""The header names must be the ones the server reads.

The server imports them from the Go SDK, so the Go constants are the source
and this compares against them.
"""

from __future__ import annotations

import re
from pathlib import Path

import paladin

GO_CLIENT = Path(__file__).resolve().parents[2] / "go" / "paladin" / "client.go"

# Go constant name → Python constant name.
PAIRS = {
    "HeaderAuthorization": "HEADER_AUTHORIZATION",
    "HeaderAPIToken": "HEADER_API_TOKEN",
    "HeaderCapability": "HEADER_CAPABILITY",
    "HeaderIdempotencyKey": "HEADER_IDEMPOTENCY_KEY",
}


def test_header_names_match_the_go_sdk() -> None:
    source = GO_CLIENT.read_text()
    for go_name, py_name in PAIRS.items():
        match = re.search(rf'^\s*{go_name}\s*=\s*"([^"]+)"', source, re.MULTILINE)
        assert match, f"{go_name} not found in {GO_CLIENT}"
        assert getattr(paladin, py_name) == match.group(1), f"{py_name} differs from Go's {go_name}"
