import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PrincipalKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PRINCIPAL_KIND_UNSPECIFIED: _ClassVar[PrincipalKind]
    PRINCIPAL_KIND_USER: _ClassVar[PrincipalKind]
    PRINCIPAL_KIND_AGENT: _ClassVar[PrincipalKind]
    PRINCIPAL_KIND_SERVICE: _ClassVar[PrincipalKind]
PRINCIPAL_KIND_UNSPECIFIED: PrincipalKind
PRINCIPAL_KIND_USER: PrincipalKind
PRINCIPAL_KIND_AGENT: PrincipalKind
PRINCIPAL_KIND_SERVICE: PrincipalKind

class CapabilityPrincipal(_message.Message):
    __slots__ = ("kind", "tenant_id", "subject", "agent_type", "agent_version", "run_id", "parent_agent_id", "model", "mcp_client")
    KIND_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    AGENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    AGENT_VERSION_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    PARENT_AGENT_ID_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    MCP_CLIENT_FIELD_NUMBER: _ClassVar[int]
    kind: PrincipalKind
    tenant_id: str
    subject: str
    agent_type: str
    agent_version: str
    run_id: str
    parent_agent_id: str
    model: str
    mcp_client: str
    def __init__(self, kind: _Optional[_Union[PrincipalKind, str]] = ..., tenant_id: _Optional[str] = ..., subject: _Optional[str] = ..., agent_type: _Optional[str] = ..., agent_version: _Optional[str] = ..., run_id: _Optional[str] = ..., parent_agent_id: _Optional[str] = ..., model: _Optional[str] = ..., mcp_client: _Optional[str] = ...) -> None: ...

class CapabilityCaveats(_message.Message):
    __slots__ = ("ops", "resource_prefixes", "resource_uris", "max_requests", "max_budget_amount", "allow_tainted_read", "idempotency_key_required", "source_ip_cidr", "unit_code")
    OPS_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_PREFIXES_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_URIS_FIELD_NUMBER: _ClassVar[int]
    MAX_REQUESTS_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_AMOUNT_FIELD_NUMBER: _ClassVar[int]
    ALLOW_TAINTED_READ_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_REQUIRED_FIELD_NUMBER: _ClassVar[int]
    SOURCE_IP_CIDR_FIELD_NUMBER: _ClassVar[int]
    UNIT_CODE_FIELD_NUMBER: _ClassVar[int]
    ops: _containers.RepeatedScalarFieldContainer[str]
    resource_prefixes: _containers.RepeatedScalarFieldContainer[str]
    resource_uris: _containers.RepeatedScalarFieldContainer[str]
    max_requests: int
    max_budget_amount: float
    allow_tainted_read: bool
    idempotency_key_required: bool
    source_ip_cidr: _containers.RepeatedScalarFieldContainer[str]
    unit_code: str
    def __init__(self, ops: _Optional[_Iterable[str]] = ..., resource_prefixes: _Optional[_Iterable[str]] = ..., resource_uris: _Optional[_Iterable[str]] = ..., max_requests: _Optional[int] = ..., max_budget_amount: _Optional[float] = ..., allow_tainted_read: _Optional[bool] = ..., idempotency_key_required: _Optional[bool] = ..., source_ip_cidr: _Optional[_Iterable[str]] = ..., unit_code: _Optional[str] = ...) -> None: ...

class Capability(_message.Message):
    __slots__ = ("id", "issuer", "subject", "audience", "caveats", "issued_at", "not_before", "expires_at", "parent_id", "generation")
    ID_FIELD_NUMBER: _ClassVar[int]
    ISSUER_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    CAVEATS_FIELD_NUMBER: _ClassVar[int]
    ISSUED_AT_FIELD_NUMBER: _ClassVar[int]
    NOT_BEFORE_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    PARENT_ID_FIELD_NUMBER: _ClassVar[int]
    GENERATION_FIELD_NUMBER: _ClassVar[int]
    id: str
    issuer: str
    subject: CapabilityPrincipal
    audience: _containers.RepeatedScalarFieldContainer[str]
    caveats: CapabilityCaveats
    issued_at: _timestamp_pb2.Timestamp
    not_before: _timestamp_pb2.Timestamp
    expires_at: _timestamp_pb2.Timestamp
    parent_id: str
    generation: int
    def __init__(self, id: _Optional[str] = ..., issuer: _Optional[str] = ..., subject: _Optional[_Union[CapabilityPrincipal, _Mapping]] = ..., audience: _Optional[_Iterable[str]] = ..., caveats: _Optional[_Union[CapabilityCaveats, _Mapping]] = ..., issued_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., not_before: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., expires_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., parent_id: _Optional[str] = ..., generation: _Optional[int] = ...) -> None: ...

class CapabilityServiceIssueRequest(_message.Message):
    __slots__ = ("subject", "audience", "caveats", "ttl_seconds", "not_before")
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    CAVEATS_FIELD_NUMBER: _ClassVar[int]
    TTL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    NOT_BEFORE_FIELD_NUMBER: _ClassVar[int]
    subject: CapabilityPrincipal
    audience: _containers.RepeatedScalarFieldContainer[str]
    caveats: CapabilityCaveats
    ttl_seconds: int
    not_before: _timestamp_pb2.Timestamp
    def __init__(self, subject: _Optional[_Union[CapabilityPrincipal, _Mapping]] = ..., audience: _Optional[_Iterable[str]] = ..., caveats: _Optional[_Union[CapabilityCaveats, _Mapping]] = ..., ttl_seconds: _Optional[int] = ..., not_before: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class CapabilityServiceIssueResponse(_message.Message):
    __slots__ = ("capability", "token")
    CAPABILITY_FIELD_NUMBER: _ClassVar[int]
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    capability: Capability
    token: str
    def __init__(self, capability: _Optional[_Union[Capability, _Mapping]] = ..., token: _Optional[str] = ...) -> None: ...

class CapabilityServiceDelegateRequest(_message.Message):
    __slots__ = ("parent_id", "subject", "audience", "caveats", "ttl_seconds", "not_before")
    PARENT_ID_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    CAVEATS_FIELD_NUMBER: _ClassVar[int]
    TTL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    NOT_BEFORE_FIELD_NUMBER: _ClassVar[int]
    parent_id: str
    subject: CapabilityPrincipal
    audience: _containers.RepeatedScalarFieldContainer[str]
    caveats: CapabilityCaveats
    ttl_seconds: int
    not_before: _timestamp_pb2.Timestamp
    def __init__(self, parent_id: _Optional[str] = ..., subject: _Optional[_Union[CapabilityPrincipal, _Mapping]] = ..., audience: _Optional[_Iterable[str]] = ..., caveats: _Optional[_Union[CapabilityCaveats, _Mapping]] = ..., ttl_seconds: _Optional[int] = ..., not_before: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class CapabilityServiceRevokeRequest(_message.Message):
    __slots__ = ("id", "reason", "cascade_children")
    ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    CASCADE_CHILDREN_FIELD_NUMBER: _ClassVar[int]
    id: str
    reason: str
    cascade_children: bool
    def __init__(self, id: _Optional[str] = ..., reason: _Optional[str] = ..., cascade_children: _Optional[bool] = ...) -> None: ...

class CapabilityServiceRevokeResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class CapabilityServiceListRequest(_message.Message):
    __slots__ = ("tenant_id", "principal_kind", "subject", "include_expired", "include_revoked", "page_size", "page_token")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_KIND_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_EXPIRED_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_REVOKED_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    principal_kind: PrincipalKind
    subject: str
    include_expired: bool
    include_revoked: bool
    page_size: int
    page_token: str
    def __init__(self, tenant_id: _Optional[str] = ..., principal_kind: _Optional[_Union[PrincipalKind, str]] = ..., subject: _Optional[str] = ..., include_expired: _Optional[bool] = ..., include_revoked: _Optional[bool] = ..., page_size: _Optional[int] = ..., page_token: _Optional[str] = ...) -> None: ...

class CapabilityServiceListResponse(_message.Message):
    __slots__ = ("capabilities", "next_page_token")
    CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    capabilities: _containers.RepeatedCompositeFieldContainer[Capability]
    next_page_token: str
    def __init__(self, capabilities: _Optional[_Iterable[_Union[Capability, _Mapping]]] = ..., next_page_token: _Optional[str] = ...) -> None: ...

class CapabilityServiceGetUsageRequest(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class CapabilityServiceGetUsageResponse(_message.Message):
    __slots__ = ("capability_id", "request_count", "spent_amount", "updated_at", "unit_code")
    CAPABILITY_ID_FIELD_NUMBER: _ClassVar[int]
    REQUEST_COUNT_FIELD_NUMBER: _ClassVar[int]
    SPENT_AMOUNT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    UNIT_CODE_FIELD_NUMBER: _ClassVar[int]
    capability_id: str
    request_count: int
    spent_amount: float
    updated_at: _timestamp_pb2.Timestamp
    unit_code: str
    def __init__(self, capability_id: _Optional[str] = ..., request_count: _Optional[int] = ..., spent_amount: _Optional[float] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., unit_code: _Optional[str] = ...) -> None: ...
