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
    "HeaderDPoP": "HEADER_DPOP",
    "HeaderIdempotencyKey": "HEADER_IDEMPOTENCY_KEY",
    "HeaderUserAgent": "HEADER_USER_AGENT",
    "HeaderRetryAfter": "HEADER_RETRY_AFTER",
    "HeaderServerVersion": "HEADER_SERVER_VERSION",
}


def test_header_names_match_the_go_sdk() -> None:
    source = GO_CLIENT.read_text()
    for go_name, py_name in PAIRS.items():
        match = re.search(rf'^\s*{go_name}\s*=\s*"([^"]+)"', source, re.MULTILINE)
        assert match, f"{go_name} not found in {GO_CLIENT}"
        assert getattr(paladin, py_name) == match.group(1), f"{py_name} differs from Go's {go_name}"


GO_AUTH = Path(__file__).resolve().parents[2] / "go" / "paladin" / "auth.go"

AUDIENCES = {
    "AudienceData": "AUDIENCE_DATA",
    "AudienceAdmin": "AUDIENCE_ADMIN",
    "AudienceIAM": "AUDIENCE_IAM",
}


def test_audiences_match_the_go_sdk() -> None:
    source = GO_AUTH.read_text()
    for go_name, py_name in AUDIENCES.items():
        match = re.search(rf'^\s*{go_name}\s*=\s*"([^"]+)"', source, re.MULTILINE)
        assert match, f"{go_name} not found in {GO_AUTH}"
        assert getattr(paladin, py_name) == match.group(1), f"{py_name} differs from Go's {go_name}"


GO_ERRORS = Path(__file__).resolve().parents[2] / "go" / "paladin" / "errors_domain.go"


def test_error_domain_matches_the_go_sdk() -> None:
    match = re.search(r'^const ErrorDomain = "([^"]+)"', GO_ERRORS.read_text(), re.MULTILINE)
    assert match, f"ErrorDomain not found in {GO_ERRORS}"
    assert match.group(1) == paladin.ERROR_DOMAIN
