import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class APIToken(_message.Message):
    __slots__ = ("name", "id", "tenant_id", "display_name", "prefix", "scopes", "audience", "roles", "expires_at", "revoked_at", "last_used_at", "created_at", "created_by", "rate_limit_rpm")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    PREFIX_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    REVOKED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_USED_AT_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    CREATED_BY_FIELD_NUMBER: _ClassVar[int]
    RATE_LIMIT_RPM_FIELD_NUMBER: _ClassVar[int]
    name: str
    id: str
    tenant_id: str
    display_name: str
    prefix: str
    scopes: _containers.RepeatedScalarFieldContainer[str]
    audience: _containers.RepeatedScalarFieldContainer[str]
    roles: _containers.RepeatedScalarFieldContainer[str]
    expires_at: _timestamp_pb2.Timestamp
    revoked_at: _timestamp_pb2.Timestamp
    last_used_at: _timestamp_pb2.Timestamp
    created_at: _timestamp_pb2.Timestamp
    created_by: str
    rate_limit_rpm: int
    def __init__(self, name: _Optional[str] = ..., id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., display_name: _Optional[str] = ..., prefix: _Optional[str] = ..., scopes: _Optional[_Iterable[str]] = ..., audience: _Optional[_Iterable[str]] = ..., roles: _Optional[_Iterable[str]] = ..., expires_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., revoked_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_used_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., created_by: _Optional[str] = ..., rate_limit_rpm: _Optional[int] = ...) -> None: ...

class APITokenServiceCreateRequest(_message.Message):
    __slots__ = ("parent", "display_name", "ttl_seconds", "scopes", "audience", "rate_limit_rpm", "roles")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    TTL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    RATE_LIMIT_RPM_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    parent: str
    display_name: str
    ttl_seconds: int
    scopes: _containers.RepeatedScalarFieldContainer[str]
    audience: _containers.RepeatedScalarFieldContainer[str]
    rate_limit_rpm: int
    roles: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, parent: _Optional[str] = ..., display_name: _Optional[str] = ..., ttl_seconds: _Optional[int] = ..., scopes: _Optional[_Iterable[str]] = ..., audience: _Optional[_Iterable[str]] = ..., rate_limit_rpm: _Optional[int] = ..., roles: _Optional[_Iterable[str]] = ...) -> None: ...

class APITokenServiceCreateResponse(_message.Message):
    __slots__ = ("api_token", "token")
    API_TOKEN_FIELD_NUMBER: _ClassVar[int]
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    api_token: APIToken
    token: str
    def __init__(self, api_token: _Optional[_Union[APIToken, _Mapping]] = ..., token: _Optional[str] = ...) -> None: ...

class APITokenServiceRevokeRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class APITokenServiceRevokeResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class APITokenServiceListRequest(_message.Message):
    __slots__ = ("parent", "include_revoked", "include_expired", "page_size", "page_token")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_REVOKED_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_EXPIRED_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    parent: str
    include_revoked: bool
    include_expired: bool
    page_size: int
    page_token: str
    def __init__(self, parent: _Optional[str] = ..., include_revoked: _Optional[bool] = ..., include_expired: _Optional[bool] = ..., page_size: _Optional[int] = ..., page_token: _Optional[str] = ...) -> None: ...

class APITokenServiceListResponse(_message.Message):
    __slots__ = ("api_tokens", "next_page_token")
    API_TOKENS_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    api_tokens: _containers.RepeatedCompositeFieldContainer[APIToken]
    next_page_token: str
    def __init__(self, api_tokens: _Optional[_Iterable[_Union[APIToken, _Mapping]]] = ..., next_page_token: _Optional[str] = ...) -> None: ...

class APITokenServiceGetSelfRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class APITokenServiceGetSelfResponse(_message.Message):
    __slots__ = ("api_token",)
    API_TOKEN_FIELD_NUMBER: _ClassVar[int]
    api_token: APIToken
    def __init__(self, api_token: _Optional[_Union[APIToken, _Mapping]] = ...) -> None: ...

class APITokenServiceGetUsageRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class APITokenServiceGetUsageResponse(_message.Message):
    __slots__ = ("name", "limit_rpm", "current_bucket_count", "previous_bucket_count", "weighted_count", "window_resets_at", "last_used_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    LIMIT_RPM_FIELD_NUMBER: _ClassVar[int]
    CURRENT_BUCKET_COUNT_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_BUCKET_COUNT_FIELD_NUMBER: _ClassVar[int]
    WEIGHTED_COUNT_FIELD_NUMBER: _ClassVar[int]
    WINDOW_RESETS_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_USED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    limit_rpm: int
    current_bucket_count: int
    previous_bucket_count: int
    weighted_count: float
    window_resets_at: _timestamp_pb2.Timestamp
    last_used_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., limit_rpm: _Optional[int] = ..., current_bucket_count: _Optional[int] = ..., previous_bucket_count: _Optional[int] = ..., weighted_count: _Optional[float] = ..., window_resets_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_used_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
