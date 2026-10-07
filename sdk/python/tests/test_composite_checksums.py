"""The composite checksum of a multipart object, against
sdk/testdata/composite_checksums.json, which the server and the Go SDK run
too: the three must compute the same bytes."""

from __future__ import annotations

import base64
import json
from pathlib import Path
from typing import Any

import pytest

from paladin.transfer import _DIGESTS, _Composite, _composite_value, _parse_composite

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "composite_checksums.json"
CASES: list[dict[str, Any]] = json.loads(VECTORS.read_text())["cases"]
# Pieces that fall inside a part, on a boundary and across several, as a
# stream from storage does.
CHUNK_SIZES = (1, 2, 3, 7, 1 << 20)


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
@pytest.mark.parametrize("chunk", CHUNK_SIZES)
def test_the_composite_matches_the_shared_vectors(case: dict[str, Any], chunk: int) -> None:
    new = _DIGESTS[case["algorithm"]]
    if new() is None:
        pytest.skip(f"{case['algorithm']} needs an extra this environment lacks")
    body = base64.b64decode(case["body_base64"])
    composite = _Composite(new, case["part_size_bytes"])
    for start in range(0, len(body), chunk):
        composite.update(body[start : start + chunk])
    assert _composite_value(composite.digest(), composite.count) == case["composite"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_a_shared_composite_parses(case: dict[str, Any]) -> None:
    new = _DIGESTS[case["algorithm"]]
    digest = new()
    if digest is None:
        pytest.skip(f"{case['algorithm']} needs an extra this environment lacks")
    parsed = _parse_composite(case["composite"], len(digest.digest()))
    assert parsed is not None and parsed[1] == len(case["parts"])


SHA256_SIZE = 32


@pytest.mark.parametrize(
    "value",
    [
        "no-separator-but-a-word",
        "AAAA",
        "D3qZqhMCO0j+h+RS7RQR8UFdX4mTed1iexltHESNUwA=-0",
        "D3qZqhMCO0j+h+RS7RQR8UFdX4mTed1iexltHESNUwA=-x",
        "not base64!-3",
        "AAAA-3",
    ],
)
def test_parse_composite_refuses(value: str) -> None:
    assert _parse_composite(value, SHA256_SIZE) is None
