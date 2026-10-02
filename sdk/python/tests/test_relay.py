"""The transport relay hands response headers to every interceptor waiting."""

from __future__ import annotations

from typing import Any

from paladin.relay import RelaySyncTransport, captured

SERVER_VERSION_HEADER = "X-Paladin-Version"
SERVER_VERSION = "4.99.0"


class _Response:
    def __init__(self, headers: dict[str, str]) -> None:
        self.headers = headers


class _Inner:
    def __init__(self, headers: dict[str, str]) -> None:
        self._headers = headers

    def execute_sync(self, request: Any) -> _Response:
        return _Response(self._headers)


def test_nested_interceptors_both_receive_the_headers() -> None:
    relay = RelaySyncTransport(_Inner({SERVER_VERSION_HEADER: SERVER_VERSION}))
    with captured() as outer, captured() as inner:
        relay.execute_sync(None)
    for holder in (outer, inner):
        assert holder.relayed
        assert holder.get(SERVER_VERSION_HEADER.lower()) == SERVER_VERSION
        assert holder.get(SERVER_VERSION_HEADER) == SERVER_VERSION


def test_a_call_outside_any_interceptor_is_passed_through() -> None:
    response = RelaySyncTransport(_Inner({})).execute_sync(None)
    assert response.headers == {}


def test_a_holder_without_a_relayed_call_says_so() -> None:
    with captured() as holder:
        pass
    assert not holder.relayed
    assert holder.get(SERVER_VERSION_HEADER) is None
