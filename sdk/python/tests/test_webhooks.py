"""Webhook signatures, against sdk/testdata/webhook_signatures.json — the
vectors the Go SDK's tests and the server's read too."""

from __future__ import annotations

import json
import re
import time
from pathlib import Path
from typing import Any

import pytest

import paladin

SHARED = Path(__file__).resolve().parents[2] / "testdata" / "webhook_signatures.json"
GO_WEBHOOK = Path(__file__).resolve().parents[2] / "go" / "paladin" / "webhook.go"
VECTORS: dict[str, Any] = json.loads(SHARED.read_text())


def test_sign_webhook_matches_the_shared_vector() -> None:
    got = paladin.sign_webhook(VECTORS["secret"], VECTORS["timestamp"], VECTORS["body"].encode())
    assert got == VECTORS["header"]


def test_default_tolerance_is_the_vectors() -> None:
    assert VECTORS["tolerance_seconds"] == paladin.DEFAULT_WEBHOOK_TOLERANCE


def test_header_name_matches_the_go_sdk() -> None:
    match = re.search(r'HeaderWebhookSignature\s*=\s*"([^"]+)"', GO_WEBHOOK.read_text())
    assert match, f"HeaderWebhookSignature not found in {GO_WEBHOOK}"
    assert paladin.HEADER_WEBHOOK_SIGNATURE == match.group(1)


@pytest.mark.parametrize("case", VECTORS["cases"], ids=lambda c: c["name"])
def test_verify_webhook_shared_cases(case: dict[str, Any]) -> None:
    args = (VECTORS["secret"], case["header"], case["body"].encode())
    if case["ok"]:
        paladin.verify_webhook(*args, now=case["now"])
    else:
        with pytest.raises(paladin.WebhookSignatureError):
            paladin.verify_webhook(*args, now=case["now"])


def test_round_trip_and_a_narrower_window() -> None:
    body = b'{"id":"1"}'
    signed = int(time.time())
    header = paladin.sign_webhook("s", signed, body)
    paladin.verify_webhook("s", header, body)
    with pytest.raises(paladin.WebhookSignatureError):
        paladin.verify_webhook("s", header, body, tolerance=1, now=signed + 2)


def test_the_error_is_a_value_error() -> None:
    assert issubclass(paladin.WebhookSignatureError, ValueError)
