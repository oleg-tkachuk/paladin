from __future__ import annotations

import asyncio
import contextlib
from datetime import datetime, timedelta, timezone
from email.utils import format_datetime

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin import (
    HEADER_API_TOKEN,
    HEADER_AUTHORIZATION,
    HEADER_CAPABILITY,
    HEADER_IDEMPOTENCY_KEY,
    HEADER_USER_AGENT,
    Client,
    Retry,
    current_idempotency_key,
    default_retryable,
    idempotency_key,
    no_idempotency_key,
    parse_retry_after,
    user_agent,
)
from paladin.iam.v1 import auth_service_pb2, health_service_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceClientSync
from paladin.iam.v1.health_service_connect import HealthServiceClient, HealthServiceClientSync

# Keeps retry tests fast; a zero delay is refused by Retry.
FAST = Retry(attempts=3, base_delay=0.001, max_delay=0.001)
# A call budget that an hour's wait cannot fit in, and a retry of a few
# milliseconds can.
CALL_TIMEOUT_MS = 2000


def _health(client: Client) -> HealthServiceClientSync:
    return HealthServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    )


def _auth(client: Client) -> AuthServiceClientSync:
    return AuthServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    )


@pytest.mark.parametrize(
    "url",
    ["", "   ", "admin.example.com", "ftp://admin.example.com", "https://"],
)
def test_rejects_bad_base_url(url: str) -> None:
    with pytest.raises(ValueError):
        Client(url)


def test_base_url_drops_trailing_slash() -> None:
    assert Client("https://admin.example.com/").base_url == "https://admin.example.com"


@pytest.mark.parametrize(
    ("attempts", "base", "cap"),
    [(0, 0.1, 1.0), (1, 0.0, 1.0), (1, 2.0, 1.0)],
)
def test_retry_rejects_bad_policy(attempts: int, base: float, cap: float) -> None:
    with pytest.raises(ValueError):
        Retry(attempts=attempts, base_delay=base, max_delay=cap)


def test_retry_delays_double_up_to_the_cap() -> None:
    assert list(Retry(attempts=5, base_delay=1.0, max_delay=3.0).delays()) == [1.0, 2.0, 3.0, 3.0]
    assert list(Retry(attempts=1).delays()) == []


def test_credentials_reach_the_server(server) -> None:  # type: ignore[no-untyped-def]
    client = Client(server.url, bearer_token="paladin_pat_abc", capability="cap-token")
    _health(client).get_version(health_service_pb2.GetVersionRequest())

    sent = server.recorder.last
    assert sent[HEADER_AUTHORIZATION.lower()] == "Bearer paladin_pat_abc"
    assert sent[HEADER_CAPABILITY.lower()] == "cap-token"
    assert HEADER_IDEMPOTENCY_KEY.lower() not in sent


def test_idempotency_key_is_scoped_to_the_block(server) -> None:  # type: ignore[no-untyped-def]
    auth = _auth(Client(server.url))
    with idempotency_key("key-1"):
        assert current_idempotency_key() == "key-1"
        auth.login(auth_service_pb2.LoginRequest())
    assert server.recorder.last[HEADER_IDEMPOTENCY_KEY.lower()] == "key-1"

    assert current_idempotency_key() is None
    auth.login(auth_service_pb2.LoginRequest())
    # Outside the block the call still has side effects, so it gets its own.
    minted = server.recorder.last[HEADER_IDEMPOTENCY_KEY.lower()]
    assert minted and minted != "key-1"


def test_empty_idempotency_key_is_none() -> None:
    with idempotency_key(""):
        assert current_idempotency_key() is None


@pytest.mark.parametrize(
    ("call", "keyed", "failures", "code", "want_calls", "want_error"),
    [
        ("get_version", False, 2, Code.UNAVAILABLE, 3, False),
        ("get_version", False, 5, Code.UNAVAILABLE, 3, True),
        ("get_version", False, 1, Code.RESOURCE_EXHAUSTED, 2, False),
        ("get_version", False, 1, Code.INVALID_ARGUMENT, 1, True),
        ("login", False, 1, Code.UNAVAILABLE, 2, False),
        ("login", True, 1, Code.UNAVAILABLE, 2, False),
    ],
    ids=[
        "side-effect free, transient, recovers",
        "side-effect free, gives up after attempts",
        "rate limited is transient",
        "permanent error is not retried",
        "mutating call carries its own key and is retried",
        "mutating call with a key is retried",
    ],
)
def test_retries(server, call, keyed, failures, code, want_calls, want_error) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = failures
    server.recorder.fail_code = code
    client = Client(server.url, retry=FAST)

    def invoke() -> None:
        if call == "get_version":
            _health(client).get_version(health_service_pb2.GetVersionRequest())
        else:
            _auth(client).login(auth_service_pb2.LoginRequest())

    def run() -> None:
        if keyed:
            with idempotency_key("key-1"):
                invoke()
        else:
            invoke()

    if want_error:
        with pytest.raises(ConnectError):
            run()
    else:
        run()
    assert server.recorder.calls == want_calls


def test_no_retry_without_a_policy(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    with pytest.raises(ConnectError):
        _health(Client(server.url)).get_version(health_service_pb2.GetVersionRequest())
    assert server.recorder.calls == 1


def test_async_client_sends_credentials_and_retries(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    client = Client(server.url, bearer_token="paladin_pat_abc", retry=FAST)

    async def call() -> None:
        async with HealthServiceClient(
            client.base_url,
            interceptors=client.async_interceptors(),
            http_client=client.async_http_client(),
        ) as health:
            await health.get_version(health_service_pb2.GetVersionRequest())

    asyncio.run(call())
    assert server.recorder.calls == 2
    assert server.recorder.last[HEADER_AUTHORIZATION.lower()] == "Bearer paladin_pat_abc"


def test_mutating_call_keeps_one_key_across_retries(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 2
    _auth(Client(server.url, retry=FAST)).login(auth_service_pb2.LoginRequest())
    keys = {h[HEADER_IDEMPOTENCY_KEY.lower()] for h in server.recorder.headers}
    assert len(server.recorder.headers) == 3
    assert len(keys) == 1, f"retries of one call sent different keys: {keys}"


def test_each_mutating_call_gets_its_own_key(server) -> None:  # type: ignore[no-untyped-def]
    auth = _auth(Client(server.url))
    auth.login(auth_service_pb2.LoginRequest())
    auth.login(auth_service_pb2.LoginRequest())
    first, second = (h[HEADER_IDEMPOTENCY_KEY.lower()] for h in server.recorder.headers)
    assert first != second


def test_retry_honours_retry_after(server) -> None:  # type: ignore[no-untyped-def]
    an_hour = "3600"
    server.recorder.failures = 1
    server.recorder.fail_code = Code.RESOURCE_EXHAUSTED
    server.recorder.retry_after = an_hour
    health = HealthServiceClientSync(
        server.url,
        interceptors=Client(server.url, retry=FAST).interceptors(),
        http_client=Client(server.url).http_client(),
        timeout_ms=CALL_TIMEOUT_MS,
    )
    with pytest.raises(ConnectError) as err:
        health.get_version(health_service_pb2.GetVersionRequest())
    assert err.value.code == Code.RESOURCE_EXHAUSTED
    assert server.recorder.calls == 1, "Retry-After was ignored"


def test_retry_not_attempted_past_the_timeout(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 10
    slow = Retry(attempts=10, base_delay=3600.0, max_delay=3600.0)
    health = HealthServiceClientSync(
        server.url,
        interceptors=Client(server.url, retry=slow).interceptors(),
        http_client=Client(server.url).http_client(),
        timeout_ms=CALL_TIMEOUT_MS,
    )
    with pytest.raises(ConnectError) as err:
        health.get_version(health_service_pb2.GetVersionRequest())
    assert err.value.code == Code.UNAVAILABLE
    assert server.recorder.calls == 1


def test_options_reach_the_server(server) -> None:  # type: ignore[no-untyped-def]
    client = Client(server.url, api_token="paladin_pat_xyz", headers={"X-Custom": "v"})
    _health(client).get_version(health_service_pb2.GetVersionRequest())
    sent = server.recorder.last
    assert sent[HEADER_API_TOKEN.lower()] == "paladin_pat_xyz"
    assert sent["x-custom"] == "v"
    assert sent[HEADER_USER_AGENT.lower()].startswith("paladin-sdk-python/")


def test_user_agent_names_the_installed_version() -> None:
    from importlib import metadata

    assert user_agent() == f"paladin-sdk-python/{metadata.version('paladin-sdk')}"


def test_no_idempotency_key_sends_none_and_is_not_retried(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    with idempotency_key("overridden"), no_idempotency_key(), pytest.raises(ConnectError):
        assert current_idempotency_key() is None
        _auth(Client(server.url, retry=FAST)).login(auth_service_pb2.LoginRequest())
    assert server.recorder.calls == 1, "a call with no key was retried"
    assert HEADER_IDEMPOTENCY_KEY.lower() not in server.recorder.last
    # The innermost block wins: a key inside the opt-out is sent.
    with no_idempotency_key(), idempotency_key("k2"):
        assert current_idempotency_key() == "k2"


def test_retry_honours_a_retry_after_date(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    server.recorder.fail_code = Code.RESOURCE_EXHAUSTED
    server.recorder.retry_after = format_datetime(
        datetime.now(timezone.utc) + timedelta(hours=1), usegmt=True
    )
    health = HealthServiceClientSync(
        server.url,
        interceptors=Client(server.url, retry=FAST).interceptors(),
        http_client=Client(server.url).http_client(),
        timeout_ms=CALL_TIMEOUT_MS,
    )
    with pytest.raises(ConnectError):
        health.get_version(health_service_pb2.GetVersionRequest())
    assert server.recorder.calls == 1, "the Retry-After date was ignored"


_NOW = datetime(2026, 10, 2, 12, 0, 0, tzinfo=timezone.utc)


@pytest.mark.parametrize(
    ("value", "want"),
    [
        ("120", 120.0),
        ("0", 0.0),
        ("-5", None),
        ("Fri, 02 Oct 2026 12:01:30 GMT", 90.0),
        ("Friday, 02-Oct-26 12:01:30 GMT", 90.0),
        ("Fri, 02 Oct 2026 11:00:00 GMT", 0.0),
        ("soon", None),
        ("", None),
    ],
)
def test_parse_retry_after(value: str, want: float | None) -> None:
    assert parse_retry_after(value, _NOW) == want


@pytest.mark.parametrize(
    ("call", "code", "want_calls"),
    [
        ("get_version", Code.INTERNAL, 2),
        ("get_version", Code.UNAVAILABLE, 1),
        ("login", Code.INTERNAL, 2),
    ],
)
def test_retryable_replaces_the_classifier(server, call: str, code: Code, want_calls: int) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    server.recorder.fail_code = code
    retry = Retry(
        attempts=3, base_delay=0.001, max_delay=0.001, retryable=lambda e: e.code == Code.INTERNAL
    )
    client = Client(server.url, retry=retry)
    with contextlib.suppress(ConnectError):
        if call == "get_version":
            _health(client).get_version(health_service_pb2.GetVersionRequest())
        else:
            _auth(client).login(auth_service_pb2.LoginRequest())
    assert server.recorder.calls == want_calls


def test_the_classifier_cannot_retry_an_unkeyed_mutating_call(server) -> None:  # type: ignore[no-untyped-def]
    server.recorder.failures = 1
    retry = Retry(attempts=3, base_delay=0.001, max_delay=0.001, retryable=lambda e: True)
    with no_idempotency_key(), pytest.raises(ConnectError):
        _auth(Client(server.url, retry=retry)).login(auth_service_pb2.LoginRequest())
    assert server.recorder.calls == 1


def test_default_retryable() -> None:
    assert default_retryable(ConnectError(Code.UNAVAILABLE, "x"))
    assert default_retryable(ConnectError(Code.RESOURCE_EXHAUSTED, "x"))
    assert not default_retryable(ConnectError(Code.INTERNAL, "x"))
