"""``connect``: clients for every service of the planes you name."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from paladin.auth import AUDIENCE_ADMIN, AUDIENCE_DATA, AUDIENCE_IAM
from paladin.client import Client
from paladin.facade import (
    AdminPlane,
    AsyncAdminPlane,
    AsyncDataPlane,
    AsyncIAMPlane,
    DataPlane,
    IAMPlane,
)
from paladin.transfer import Transfer


@dataclass(frozen=True)
class Endpoints:
    """The base URLs of the planes. A plane left out is not connected."""

    data: str | None = None
    admin: str | None = None
    iam: str | None = None

    def __post_init__(self) -> None:
        if not (self.data or self.admin or self.iam):
            raise ValueError("Endpoints needs at least one plane's URL")


@dataclass(frozen=True)
class Paladin:
    """A client for every service of every connected plane; None for the others."""

    data: DataPlane | None
    admin: AdminPlane | None
    iam: IAMPlane | None


@dataclass(frozen=True)
class AsyncPaladin:
    data: AsyncDataPlane | None
    admin: AsyncAdminPlane | None
    iam: AsyncIAMPlane | None


def _client(url: str, audience: str, client_options: dict[str, Any]) -> Client:
    token_source = client_options.get("token_source")
    return Client(url, **client_options, audience=audience if token_source is not None else None)


def connect(
    endpoints: Endpoints,
    *,
    transport: dict[str, Any] | None = None,
    transfer: Transfer | None = None,
    **client_options: Any,
) -> Paladin:
    """Synchronous clients for the planes in ``endpoints``.

    ``client_options`` are ``Client``'s — credentials, ``retry``, ``headers``;
    a ``token_source`` sends each plane the token for its own audience.
    ``transport`` is passed to every generated client: ``timeout_ms``,
    ``http_client``, ``proto_json`` and the like. ``transfer`` sends the
    presigned requests of ``upload`` and ``download``; a shared default when
    left out.
    """
    extra = transport or {}

    def plane(url: str | None, audience: str, kind: Any, **more: Any) -> Any:
        if not url:
            return None
        client = _client(url, audience, client_options)
        return kind(client.base_url, client.interceptors(), **more, **extra)

    return Paladin(
        data=plane(endpoints.data, AUDIENCE_DATA, DataPlane, transfer=transfer),
        admin=plane(endpoints.admin, AUDIENCE_ADMIN, AdminPlane),
        iam=plane(endpoints.iam, AUDIENCE_IAM, IAMPlane),
    )


def connect_async(
    endpoints: Endpoints,
    *,
    transport: dict[str, Any] | None = None,
    transfer: Transfer | None = None,
    **client_options: Any,
) -> AsyncPaladin:
    """``connect`` for asyncio: the generated async clients, and an
    ``AsyncSession`` as the token source."""
    extra = transport or {}

    def plane(url: str | None, audience: str, kind: Any, **more: Any) -> Any:
        if not url:
            return None
        client = _client(url, audience, client_options)
        return kind(client.base_url, client.async_interceptors(), **more, **extra)

    return AsyncPaladin(
        data=plane(endpoints.data, AUDIENCE_DATA, AsyncDataPlane, transfer=transfer),
        admin=plane(endpoints.admin, AUDIENCE_ADMIN, AsyncAdminPlane),
        iam=plane(endpoints.iam, AUDIENCE_IAM, AsyncIAMPlane),
    )
