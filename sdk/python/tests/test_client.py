from __future__ import annotations

import asyncio

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin import (
    HEADER_AUTHORIZATION,
    HEADER_CAPABILITY,
    HEADER_IDEMPOTENCY_KEY,
    Client,
    Retry,
    current_idempotency_key,
    idempotency_key,
)
from paladin.iam.v1 import auth_service_pb2, health_service_pb2
from paladin.iam.v1.auth_service_connect import AuthServiceClientSync
from paladin.iam.v1.health_service_connect import HealthServiceClient, HealthServiceClientSync

# Keeps retry tests fast; a zero delay is refused by Retry.
FAST = Retry(attempts=3, base_delay=0.001, max_delay=0.001)


def _health(client: Client) -> HealthServiceClientSync:
    return HealthServiceClientSync(client.base_url, interceptors=client.interceptors())


def _auth(client: Client) -> AuthServiceClientSync:
    return AuthServiceClientSync(client.base_url, interceptors=client.interceptors())


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
    assert HEADER_IDEMPOTENCY_KEY.lower() not in server.recorder.last


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
        ("login", False, 1, Code.UNAVAILABLE, 1, True),
        ("login", True, 1, Code.UNAVAILABLE, 2, False),
    ],
    ids=[
        "side-effect free, transient, recovers",
        "side-effect free, gives up after attempts",
        "rate limited is transient",
        "permanent error is not retried",
        "mutating call without a key is not retried",
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
            client.base_url, interceptors=client.async_interceptors()
        ) as health:
            await health.get_version(health_service_pb2.GetVersionRequest())

    asyncio.run(call())
    assert server.recorder.calls == 2
    assert server.recorder.last[HEADER_AUTHORIZATION.lower()] == "Bearer paladin_pat_abc"
