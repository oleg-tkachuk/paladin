"""Typed errors: what a failed call was, without matching codes or messages.

Every failed unary call through a ``Client``'s interceptors raises a
``PaladinError`` subclass for its code — ``NotFoundError``,
``VersionConflictError``, … — which is a ``ConnectError`` still, so an
``except ConnectError`` keeps working. It carries the server's reason
(``paladin.common.v1.ErrorReason``, from the ``google.rpc.ErrorInfo`` in
domain ``ERROR_DOMAIN``), the ``Retry-After``, the procedure and both
releases.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.rpc import error_details_pb2

from paladin.common.v1 import error_reason_pb2

ERROR_DOMAIN = "paladin"
"""The ErrorInfo domain of the reasons the server attaches."""
HEADER_SERVER_VERSION = "X-Paladin-Version"
"""The server's release, on every response."""
_UNKNOWN_VERSION = "unknown version"
_UNSPECIFIED = error_reason_pb2.ERROR_REASON_UNSPECIFIED


class PaladinError(ConnectError):
    """A failed Paladin call.

    ``procedure`` is the RPC, ``/paladin.data.v1.ObjectService/GetObject``;
    ``reason`` the server's ``ErrorReason`` value, ``ERROR_REASON_UNSPECIFIED``
    when it sent none or one this SDK predates; ``retry_after`` the seconds
    the server asked to wait, or None; ``server_version`` and ``sdk_version``
    the two releases; ``decoded_details`` the details it could unpack.
    """

    def __init__(
        self,
        code: Code,
        message: str,
        details: Any = (),
        *,
        procedure: str = "",
        reason: int = _UNSPECIFIED,
        retry_after: float | None = None,
        server_version: str = "",
        sdk_version: str = "",
        decoded_details: list[Any] | None = None,
    ) -> None:
        super().__init__(code, message, details)
        self.procedure = procedure
        self.reason = reason
        self.retry_after = retry_after
        self.server_version = server_version
        self.sdk_version = sdk_version
        self.decoded_details = decoded_details or []

    @property
    def reason_name(self) -> str:
        """``reason`` as its enum value name, e.g. ``ERROR_REASON_NOT_FOUND``."""
        return error_reason_pb2.ErrorReason.Name(self.reason)


class NotFoundError(PaladinError):
    """``NOT_FOUND``."""


class InvalidArgumentError(PaladinError):
    """The request is malformed or names something invalid; it will fail the
    same way however often it is sent. ``reason`` says which rule it broke."""


class AlreadyExistsError(PaladinError):
    """``ALREADY_EXISTS``."""


class PermissionDeniedError(PaladinError):
    """``PERMISSION_DENIED``."""


class FailedPreconditionError(PaladinError):
    """``FAILED_PRECONDITION``."""


class VersionConflictError(PaladinError):
    """``ABORTED``: the resource changed since it was read. Read it again and
    retry the change."""


class ResourceExhaustedError(PaladinError):
    """``RESOURCE_EXHAUSTED``; ``retry_after`` is how long the server asked to wait."""


class UnauthenticatedError(PaladinError):
    """``UNAUTHENTICATED``."""


class ContractSkewError(PaladinError):
    """``UNIMPLEMENTED``: the server does not implement the call — it is older
    than the SDK. The message names the procedure and both releases."""

    def __str__(self) -> str:
        server = self.server_version or _UNKNOWN_VERSION
        return (
            f"{self.procedure} is not implemented by the server "
            f"(server {server}, sdk {self.sdk_version}): {self.message}"
        )


_KINDS: Mapping[Code, type[PaladinError]] = {
    Code.INVALID_ARGUMENT: InvalidArgumentError,
    Code.NOT_FOUND: NotFoundError,
    Code.ALREADY_EXISTS: AlreadyExistsError,
    Code.PERMISSION_DENIED: PermissionDeniedError,
    Code.FAILED_PRECONDITION: FailedPreconditionError,
    Code.ABORTED: VersionConflictError,
    Code.RESOURCE_EXHAUSTED: ResourceExhaustedError,
    Code.UNAUTHENTICATED: UnauthenticatedError,
    Code.UNIMPLEMENTED: ContractSkewError,
}


def reason(err: BaseException) -> int:
    """The server's ``ErrorReason`` for ``err``; ``ERROR_REASON_UNSPECIFIED``
    when it is not a Paladin error or carries none."""
    return err.reason if isinstance(err, PaladinError) else _UNSPECIFIED


def _decode(details: Any) -> tuple[int, list[Any]]:
    """The reason in the details' ErrorInfo, and every ErrorInfo decoded."""
    found = _UNSPECIFIED
    decoded: list[Any] = []
    for packed in details:
        if not packed.Is(error_details_pb2.ErrorInfo.DESCRIPTOR):
            continue
        info = error_details_pb2.ErrorInfo()
        packed.Unpack(info)
        decoded.append(info)
        if info.domain == ERROR_DOMAIN:
            try:
                found = error_reason_pb2.ErrorReason.Value(info.reason)
            except ValueError:
                found = _UNSPECIFIED  # a reason newer than this SDK
    return found, decoded


def convert(
    err: ConnectError,
    procedure: str,
    headers: Any,
    sdk_version: str,
    parse_retry_after: Any,
) -> ConnectError:
    """``err`` as the ``PaladinError`` for its code — the base class for a
    code with no kind of its own, so ``reason``, ``retry_after`` and both
    releases are there whatever the code, as in the Go SDK."""
    if isinstance(err, PaladinError):
        return err
    kind = _KINDS.get(err.code, PaladinError)
    found, decoded = _decode(err.details)
    after = headers.get("retry-after")
    return kind(
        err.code,
        err.message,
        err.details,
        procedure=procedure,
        reason=found,
        retry_after=parse_retry_after(after) if after is not None else None,
        server_version=headers.get(HEADER_SERVER_VERSION.lower()) or "",
        sdk_version=sdk_version,
        decoded_details=decoded,
    )
