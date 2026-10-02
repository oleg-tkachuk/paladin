"""Python client for the Paladin API.

The generated stubs live in ``paladin.admin.v1``, ``paladin.data.v1``,
``paladin.iam.v1`` and ``paladin.common.v1``. ``paladin.connect`` builds a
client for every service of each plane; ``paladin.client`` holds what every
call needs, and ``paladin.auth`` the tokens.
"""

from paladin.auth import (
    AUDIENCE_ADMIN,
    AUDIENCE_DATA,
    AUDIENCE_IAM,
    TOKEN_REFRESH_MARGIN,
    AsyncSession,
    Session,
    StaticToken,
)
from paladin.client import (
    DEFAULT_RETRY_BASE_DELAY,
    DEFAULT_RETRY_MAX_DELAY,
    HEADER_API_TOKEN,
    HEADER_AUTHORIZATION,
    HEADER_CAPABILITY,
    HEADER_IDEMPOTENCY_KEY,
    HEADER_RETRY_AFTER,
    HEADER_USER_AGENT,
    Client,
    Retry,
    current_idempotency_key,
    idempotency_key,
    user_agent,
)
from paladin.connect import AsyncPaladin, Endpoints, Paladin, connect, connect_async

__all__ = [
    "AUDIENCE_ADMIN",
    "AUDIENCE_DATA",
    "AUDIENCE_IAM",
    "DEFAULT_RETRY_BASE_DELAY",
    "DEFAULT_RETRY_MAX_DELAY",
    "HEADER_API_TOKEN",
    "HEADER_AUTHORIZATION",
    "HEADER_CAPABILITY",
    "HEADER_IDEMPOTENCY_KEY",
    "HEADER_RETRY_AFTER",
    "HEADER_USER_AGENT",
    "TOKEN_REFRESH_MARGIN",
    "AsyncPaladin",
    "AsyncSession",
    "Client",
    "Endpoints",
    "Paladin",
    "Retry",
    "Session",
    "StaticToken",
    "connect",
    "connect_async",
    "current_idempotency_key",
    "idempotency_key",
    "user_agent",
]
