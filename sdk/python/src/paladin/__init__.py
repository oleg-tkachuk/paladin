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
from paladin.workflows import (
    DEFAULT_MAX_POLL_INTERVAL,
    DEFAULT_MULTIPART_THRESHOLD,
    DEFAULT_PART_CONCURRENCY,
    DEFAULT_POLL_INTERVAL,
    OperationFailed,
    TransferError,
    apages,
    await_operation,
    download,
    mask,
    pages,
    upload,
    wait,
)

__all__ = [
    "AUDIENCE_ADMIN",
    "AUDIENCE_DATA",
    "AUDIENCE_IAM",
    "DEFAULT_MAX_POLL_INTERVAL",
    "DEFAULT_MULTIPART_THRESHOLD",
    "DEFAULT_PART_CONCURRENCY",
    "DEFAULT_POLL_INTERVAL",
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
    "OperationFailed",
    "Paladin",
    "Retry",
    "Session",
    "StaticToken",
    "TransferError",
    "apages",
    "await_operation",
    "connect",
    "connect_async",
    "current_idempotency_key",
    "download",
    "idempotency_key",
    "mask",
    "pages",
    "upload",
    "user_agent",
    "wait",
]
