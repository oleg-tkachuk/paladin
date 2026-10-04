"""paladin.attenuate, against sdk/testdata/biscuit_vocabulary.json — the names
capability/'s tests read too — and the cross-language case in
sdk/testdata/biscuit/, whose Python-made token capability/ verifies.

    PALADIN_WRITE_BISCUIT_FIXTURE=1 uv run pytest tests/test_biscuit.py

rewrites python.biscuit from seed.biscuit.
"""

from __future__ import annotations

import importlib.util
import json
import os
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

import pytest

import paladin
from paladin import biscuit as pb

TESTDATA = Path(__file__).resolve().parents[2] / "testdata"
VOCABULARY = TESTDATA / "biscuit_vocabulary.json"
FIXTURES = TESTDATA / "biscuit"
SEED, PYTHON, CASE = (FIXTURES / n for n in ("seed.biscuit", "python.biscuit", "attenuation.json"))
WRITE_FIXTURE = "PALADIN_WRITE_BISCUIT_FIXTURE"
JKT = "nhJL-4fvkZxa3j-NAU29y8N1wqcS_E-ZMtKfgT23tV0"
# A Biscuit appends each attenuation as one more block after the authority.
AUTHORITY_BLOCKS = 1
HAS_BISCUIT = importlib.util.find_spec("biscuit_auth") is not None
needs_biscuit = pytest.mark.skipif(not HAS_BISCUIT, reason="the biscuit extra is not installed")


def _seed() -> str:
    return SEED.read_text().strip()


def _case() -> dict[str, Any]:
    case = json.loads(CASE.read_text())
    args = dict(case["attenuate"])
    args["expires_at"] = datetime.fromisoformat(args["expires_at"].replace("Z", "+00:00"))
    return args


def _blocks(token: str) -> Any:
    import biscuit_auth

    return biscuit_auth.UnverifiedBiscuit.from_base64(token)


def _facts(token: str, block: int) -> set[str]:
    source = _blocks(token).block_source(block)
    return {line.strip().rstrip(";") for line in source.splitlines() if line.strip()}


def _expected_facts(args: dict[str, Any]) -> set[str]:
    expires = args["expires_at"].astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    return (
        {f'{pb.FACT_OP}("{v}")' for v in args["ops"]}
        | {f'{pb.FACT_RESOURCE_PREFIX}("{v}")' for v in args["resource_prefixes"]}
        | {f'{pb.FACT_PLANE}("{v}")' for v in args["planes"]}
        | {f"{pb.FACT_EXPIRES}({expires})", f'{pb.FACT_BIND}("{args["bind_jkt"]}")'}
        | {
            f"{pb.FACT_MAX_REQUESTS}({args['max_requests']})",
            f"{pb.FACT_MAX_BUDGET_MICROS}({args['max_budget_micros']})",
        }
    )


def test_vocabulary_matches_shared_spec() -> None:
    spec = json.loads(VOCABULARY.read_text())
    assert spec["facts"] == {
        pb.FACT_OP: "string",
        pb.FACT_RESOURCE_PREFIX: "string",
        pb.FACT_RESOURCE_URI: "string",
        pb.FACT_PLANE: "string",
        pb.FACT_EXPIRES: "date",
        pb.FACT_BIND: "string",
        pb.FACT_MAX_REQUESTS: "integer",
        pb.FACT_MAX_BUDGET_MICROS: "integer",
    }
    assert spec["authority"]["fact"] == pb.FACT_CAPABILITY
    assert spec["authority"]["root_claim"] == pb.ROOT_CLAIM
    assert spec["replace_together"] == [[pb.FACT_RESOURCE_PREFIX, pb.FACT_RESOURCE_URI]]
    assert spec["bind_thumbprint_bytes"] == pb.THUMBPRINT_BYTES


def test_exported() -> None:
    assert paladin.attenuate is pb.attenuate


@needs_biscuit
def test_attenuate_appends_one_block_of_the_shared_case() -> None:
    seed, args = _seed(), _case()
    out = paladin.attenuate(seed, **args)
    # The server decodes base64url without padding.
    assert not set(out) & set("=.+/")
    assert _blocks(out).block_count() == AUTHORITY_BLOCKS + 1
    assert _facts(out, AUTHORITY_BLOCKS) == _expected_facts(args)
    assert _blocks(seed).block_count() == AUTHORITY_BLOCKS


@needs_biscuit
def test_checked_in_fixture_is_the_shared_case() -> None:
    """python.biscuit — what capability/ verifies — carries the case's block."""
    if os.environ.get(WRITE_FIXTURE):
        PYTHON.write_text(paladin.attenuate(_seed(), **_case()) + "\n")
    token = PYTHON.read_text().strip()
    assert _blocks(token).block_source(0) == _blocks(_seed()).block_source(0)
    assert _blocks(token).block_count() == AUTHORITY_BLOCKS + 1
    assert _facts(token, AUTHORITY_BLOCKS) == _expected_facts(_case())


@needs_biscuit
def test_attenuations_chain() -> None:
    once = paladin.attenuate(_seed(), ops=["get", "list"])
    twice = paladin.attenuate(once, resource_uris=["paladin://t/c/k"])
    assert _blocks(twice).block_count() == AUTHORITY_BLOCKS + 2
    assert _facts(twice, AUTHORITY_BLOCKS + 1) == {f'{pb.FACT_RESOURCE_URI}("paladin://t/c/k")'}


@needs_biscuit
def test_expiry_is_kept_to_the_second_in_utc() -> None:
    east = timezone(timedelta(hours=3))
    out = paladin.attenuate(
        _seed(), expires_at=datetime(2026, 1, 1, 3, 15, 0, 999_999, tzinfo=east)
    )
    assert _facts(out, AUTHORITY_BLOCKS) == {f"{pb.FACT_EXPIRES}(2026-01-01T00:15:00Z)"}


@needs_biscuit
@pytest.mark.parametrize("token", ["AAAA", "not base64 at all!"])
def test_refuses_what_is_not_a_paladin_biscuit(token: str) -> None:
    with pytest.raises(ValueError, match="not a Paladin Biscuit"):
        paladin.attenuate(token, ops=["get"])


@needs_biscuit
def test_refuses_a_token_whose_chain_is_broken() -> None:
    seed = _seed()
    # The middle, not the end: the last character may hold only padding bits.
    mid = len(seed) // 2
    flipped = seed[:mid] + ("A" if seed[mid] != "A" else "B") + seed[mid + 1 :]
    with pytest.raises(ValueError, match="not a Paladin Biscuit"):
        paladin.attenuate(flipped, ops=["get"])


@pytest.mark.parametrize(
    ("kwargs", "error", "match"),
    [
        ({"ops": "get"}, TypeError, "list of strings"),
        ({"planes": [1]}, TypeError, "strings"),
        ({"resource_prefixes": [""]}, ValueError, "non-empty"),
        ({"resource_uris": [""]}, ValueError, "non-empty"),
        # Naive on purpose: the case under test.
        ({"expires_at": datetime(2026, 1, 1)}, ValueError, "timezone-aware"),  # noqa: DTZ001
        ({"expires_at": "2026-01-01"}, TypeError, "datetime"),
        ({"bind_jkt": "short"}, ValueError, "thumbprint"),
        ({"bind_jkt": JKT + "="}, ValueError, "thumbprint"),
        ({"max_requests": 0}, ValueError, "positive"),
        ({"max_budget_micros": -1}, ValueError, "positive"),
        ({"max_requests": True}, TypeError, "int"),
        ({"max_budget_micros": 1.5}, TypeError, "int"),
    ],
)
def test_refuses_malformed_arguments(
    kwargs: dict[str, Any], error: type[Exception], match: str
) -> None:
    with pytest.raises(error, match=match):
        paladin.attenuate("AAAA", **kwargs)


@pytest.mark.parametrize("token", ["", "header.claims.signature"])
def test_refuses_a_jwt_or_nothing(token: str) -> None:
    with pytest.raises(ValueError, match="not a Biscuit token"):
        paladin.attenuate(token, ops=["get"])


@pytest.mark.skipif(HAS_BISCUIT, reason="the biscuit extra is installed")
def test_names_the_extra_without_it() -> None:
    with pytest.raises(ImportError, match=r"paladin-sdk\[biscuit\]"):
        paladin.attenuate("AAAA", ops=["get"])
