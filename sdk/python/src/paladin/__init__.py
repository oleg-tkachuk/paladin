"""Python client for the Paladin API.

The generated stubs live in ``paladin.admin.v1``, ``paladin.data.v1``,
``paladin.iam.v1`` and ``paladin.common.v1``; ``paladin.client`` holds the thin
client that configures them.
"""

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

__all__ = [
    "DEFAULT_RETRY_BASE_DELAY",
    "DEFAULT_RETRY_MAX_DELAY",
    "HEADER_API_TOKEN",
    "HEADER_AUTHORIZATION",
    "HEADER_CAPABILITY",
    "HEADER_IDEMPOTENCY_KEY",
    "HEADER_RETRY_AFTER",
    "HEADER_USER_AGENT",
    "Client",
    "Retry",
    "current_idempotency_key",
    "idempotency_key",
    "user_agent",
]
