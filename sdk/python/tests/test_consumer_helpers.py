"""capability_source, ensure and the capability cache: what services acting
for many callers, and provisioners run at every boot, wrote for themselves."""

from __future__ import annotations

import asyncio
import threading
from contextvars import ContextVar
from datetime import datetime, timedelta, timezone

import pytest
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from cryptography.hazmat.primitives.asymmetric import ed25519

from paladin import (
    CAPABILITY_REFRESH_MARGIN,
    HEADER_CAPABILITY,
    HEADER_DPOP,
    AsyncCapabilityCache,
    CapabilityCache,
    Client,
    NoCapabilityKeyError,
    aensure,
    ensure,
)
from paladin.iam.v1 import health_service_pb2
from paladin.iam.v1.health_service_connect import HealthServiceClient, HealthServiceClientSync

T0 = datetime(2026, 1, 1, tzinfo=timezone.utc)
LIFETIME = timedelta(minutes=5)
caller: ContextVar[str] = ContextVar("caller", default="")


def _call(client: Client) -> None:
    HealthServiceClientSync(
        client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
    ).get_version(health_service_pb2.GetVersionRequest())


def test_capability_source_is_asked_on_every_call(server) -> None:  # type: ignore[no-untyped-def]
    client = Client(server.url, capability="static", capability_source=lambda: caller.get())
    for name in ("cap-a", "cap-b"):
        caller.set(name)
        _call(client)
        assert server.recorder.last[HEADER_CAPABILITY.lower()] == name
    caller.set("")
    _call(client)
    assert server.recorder.last[HEADER_CAPABILITY.lower()] == "static", (
        "an empty source must leave the static capability"
    )


def test_dpop_signs_over_the_sourced_capability(server) -> None:  # type: ignore[no-untyped-def]
    client = Client(
        server.url,
        capability_source=lambda: "cap-src",
        dpop_key=ed25519.Ed25519PrivateKey.generate(),
    )
    _call(client)
    assert server.recorder.last[HEADER_CAPABILITY.lower()] == "cap-src"
    assert HEADER_DPOP.lower() in server.recorder.last, "no proof for a sourced capability"


def test_async_capability_source_may_be_a_coroutine(server) -> None:  # type: ignore[no-untyped-def]
    async def source() -> str:
        return "cap-async"

    client = Client(server.url, capability_source=source)

    async def call() -> None:
        async with HealthServiceClient(
            client.base_url,
            interceptors=client.async_interceptors(),
            http_client=client.async_http_client(),
        ) as health:
            await health.get_version(health_service_pb2.GetVersionRequest())

    asyncio.run(call())
    assert server.recorder.last[HEADER_CAPABILITY.lower()] == "cap-async"


def _raising(code: Code):  # type: ignore[no-untyped-def]
    def call() -> str:
        raise ConnectError(code, code.name)

    return call


@pytest.mark.parametrize(
    ("gets", "create", "want", "created"),
    [
        (["found"], None, "found", False),
        ([Code.NOT_FOUND], "made", "made", True),
        ([Code.NOT_FOUND, "theirs"], Code.ALREADY_EXISTS, "theirs", False),
    ],
)
def test_ensure(gets, create, want, created) -> None:  # type: ignore[no-untyped-def]
    answers = iter(gets)

    def get() -> str:
        a = next(answers)
        if isinstance(a, Code):
            raise ConnectError(a, a.name)
        return a

    def make() -> str:
        if isinstance(create, Code):
            raise ConnectError(create, create.name)
        assert create is not None, "created a resource that exists"
        return create

    assert ensure(get, make) == (want, created)
    answers = iter(gets)
    assert asyncio.run(aensure(_async(get), _async(make))) == (want, created)


def _async(fn):  # type: ignore[no-untyped-def]
    async def call():  # type: ignore[no-untyped-def]
        return fn()

    return call


@pytest.mark.parametrize(
    ("get", "create", "code"),
    [
        (_raising(Code.PERMISSION_DENIED), lambda: "x", Code.PERMISSION_DENIED),
        (_raising(Code.NOT_FOUND), _raising(Code.INVALID_ARGUMENT), Code.INVALID_ARGUMENT),
    ],
)
def test_ensure_raises_other_errors(get, create, code) -> None:  # type: ignore[no-untyped-def]
    with pytest.raises(ConnectError) as err:
        ensure(get, create)
    assert err.value.code == code


class _Clock:
    def __init__(self) -> None:
        self.now = T0

    def __call__(self) -> datetime:
        return self.now


def test_cache_keeps_a_capability_until_the_margin() -> None:
    clock, minted = _Clock(), []

    def mint(key: str) -> tuple[str, datetime]:
        minted.append(key)
        return f"{key}-{len(minted)}", clock.now + LIFETIME

    cache = CapabilityCache(mint, clock=clock)
    assert cache.token("t1") == "t1-1"
    clock.now = T0 + LIFETIME - CAPABILITY_REFRESH_MARGIN - timedelta(seconds=1)
    assert cache.token("t1") == "t1-1"
    assert cache.token("t2") == "t2-2", "keys share a capability"
    clock.now = T0 + LIFETIME - CAPABILITY_REFRESH_MARGIN
    assert cache.token("t1") == "t1-3", "served a capability inside the margin"
    cache.invalidate("t1")
    assert cache.token("t1") == "t1-4"


def test_cache_does_not_keep_a_failed_mint() -> None:
    attempts = []

    def mint(key: str) -> tuple[str, datetime]:
        attempts.append(key)
        if len(attempts) == 1:
            raise ConnectError(Code.UNAVAILABLE, "iam down")
        return "ok", T0 + LIFETIME

    cache = CapabilityCache(mint, clock=_Clock())
    with pytest.raises(ConnectError):
        cache.token("t")
    assert cache.token("t") == "ok"


def test_cache_refuses_an_empty_key() -> None:
    with pytest.raises(NoCapabilityKeyError):
        CapabilityCache(lambda k: ("x", T0 + LIFETIME), clock=_Clock()).token("")


def test_concurrent_callers_wait_for_one_mint() -> None:
    callers, minted, release = 8, [], threading.Event()

    def mint(key: str) -> tuple[str, datetime]:
        minted.append(key)
        release.wait()
        return "shared", T0 + LIFETIME

    cache = CapabilityCache(mint, clock=_Clock())
    got: list[str] = []
    threads = [
        threading.Thread(target=lambda: got.append(cache.token("t"))) for _ in range(callers)
    ]
    for t in threads:
        t.start()
    release.set()
    for t in threads:
        t.join()
    assert got == ["shared"] * callers
    assert minted == ["t"]


def test_async_cache_mints_once_for_concurrent_callers() -> None:
    callers, minted = 8, []

    async def mint(key: str) -> tuple[str, datetime]:
        minted.append(key)
        await asyncio.sleep(0)
        return "shared", T0 + LIFETIME

    async def run() -> list[str]:
        cache = AsyncCapabilityCache(mint, clock=_Clock())
        return await asyncio.gather(*(cache.token("t") for _ in range(callers)))

    assert asyncio.run(run()) == ["shared"] * callers
    assert minted == ["t"]
