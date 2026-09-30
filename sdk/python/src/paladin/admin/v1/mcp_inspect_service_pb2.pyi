import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetBridgeStatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetBridgeStatusResponse(_message.Message):
    __slots__ = ("reachable", "error", "upstreams", "sessions", "checked_at")
    REACHABLE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    UPSTREAMS_FIELD_NUMBER: _ClassVar[int]
    SESSIONS_FIELD_NUMBER: _ClassVar[int]
    CHECKED_AT_FIELD_NUMBER: _ClassVar[int]
    reachable: bool
    error: str
    upstreams: _containers.RepeatedCompositeFieldContainer[MCPUpstreamHealth]
    sessions: int
    checked_at: _timestamp_pb2.Timestamp
    def __init__(self, reachable: _Optional[bool] = ..., error: _Optional[str] = ..., upstreams: _Optional[_Iterable[_Union[MCPUpstreamHealth, _Mapping]]] = ..., sessions: _Optional[int] = ..., checked_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MCPUpstreamHealth(_message.Message):
    __slots__ = ("name", "url", "reachable", "error", "latency_ms")
    NAME_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    REACHABLE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    LATENCY_MS_FIELD_NUMBER: _ClassVar[int]
    name: str
    url: str
    reachable: bool
    error: str
    latency_ms: int
    def __init__(self, name: _Optional[str] = ..., url: _Optional[str] = ..., reachable: _Optional[bool] = ..., error: _Optional[str] = ..., latency_ms: _Optional[int] = ...) -> None: ...

class MCPInspectRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListSessionsRequest(_message.Message):
    __slots__ = ("page",)
    PAGE_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListSessionsResponse(_message.Message):
    __slots__ = ("sessions", "page")
    SESSIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    sessions: _containers.RepeatedCompositeFieldContainer[MCPSession]
    page: _pagination_pb2.PageResponse
    def __init__(self, sessions: _Optional[_Iterable[_Union[MCPSession, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class MCPSession(_message.Message):
    __slots__ = ("id", "agent_subject", "started_at", "last_seen", "tool_call_count", "request_count")
    ID_FIELD_NUMBER: _ClassVar[int]
    AGENT_SUBJECT_FIELD_NUMBER: _ClassVar[int]
    STARTED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_COUNT_FIELD_NUMBER: _ClassVar[int]
    REQUEST_COUNT_FIELD_NUMBER: _ClassVar[int]
    id: str
    agent_subject: str
    started_at: _timestamp_pb2.Timestamp
    last_seen: _timestamp_pb2.Timestamp
    tool_call_count: int
    request_count: int
    def __init__(self, id: _Optional[str] = ..., agent_subject: _Optional[str] = ..., started_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_seen: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., tool_call_count: _Optional[int] = ..., request_count: _Optional[int] = ...) -> None: ...

class MCPInspectResponse(_message.Message):
    __slots__ = ("profiles", "always_deny", "tool_catalog", "upstreams", "transports")
    PROFILES_FIELD_NUMBER: _ClassVar[int]
    ALWAYS_DENY_FIELD_NUMBER: _ClassVar[int]
    TOOL_CATALOG_FIELD_NUMBER: _ClassVar[int]
    UPSTREAMS_FIELD_NUMBER: _ClassVar[int]
    TRANSPORTS_FIELD_NUMBER: _ClassVar[int]
    profiles: _containers.RepeatedCompositeFieldContainer[MCPProfile]
    always_deny: _containers.RepeatedScalarFieldContainer[str]
    tool_catalog: _containers.RepeatedCompositeFieldContainer[MCPTool]
    upstreams: MCPUpstreams
    transports: MCPTransports
    def __init__(self, profiles: _Optional[_Iterable[_Union[MCPProfile, _Mapping]]] = ..., always_deny: _Optional[_Iterable[str]] = ..., tool_catalog: _Optional[_Iterable[_Union[MCPTool, _Mapping]]] = ..., upstreams: _Optional[_Union[MCPUpstreams, _Mapping]] = ..., transports: _Optional[_Union[MCPTransports, _Mapping]] = ...) -> None: ...

class MCPProfile(_message.Message):
    __slots__ = ("name", "tools", "raw_patterns", "deny", "source")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    RAW_PATTERNS_FIELD_NUMBER: _ClassVar[int]
    DENY_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    name: str
    tools: _containers.RepeatedScalarFieldContainer[str]
    raw_patterns: _containers.RepeatedScalarFieldContainer[str]
    deny: _containers.RepeatedScalarFieldContainer[str]
    source: str
    def __init__(self, name: _Optional[str] = ..., tools: _Optional[_Iterable[str]] = ..., raw_patterns: _Optional[_Iterable[str]] = ..., deny: _Optional[_Iterable[str]] = ..., source: _Optional[str] = ...) -> None: ...

class MCPTool(_message.Message):
    __slots__ = ("name", "audience", "description", "capability_op", "mutates")
    NAME_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    CAPABILITY_OP_FIELD_NUMBER: _ClassVar[int]
    MUTATES_FIELD_NUMBER: _ClassVar[int]
    name: str
    audience: str
    description: str
    capability_op: str
    mutates: bool
    def __init__(self, name: _Optional[str] = ..., audience: _Optional[str] = ..., description: _Optional[str] = ..., capability_op: _Optional[str] = ..., mutates: _Optional[bool] = ...) -> None: ...

class MCPUpstreams(_message.Message):
    __slots__ = ("admin_url", "data_url", "iam_url")
    ADMIN_URL_FIELD_NUMBER: _ClassVar[int]
    DATA_URL_FIELD_NUMBER: _ClassVar[int]
    IAM_URL_FIELD_NUMBER: _ClassVar[int]
    admin_url: str
    data_url: str
    iam_url: str
    def __init__(self, admin_url: _Optional[str] = ..., data_url: _Optional[str] = ..., iam_url: _Optional[str] = ...) -> None: ...

class MCPTransports(_message.Message):
    __slots__ = ("stdio", "http")
    STDIO_FIELD_NUMBER: _ClassVar[int]
    HTTP_FIELD_NUMBER: _ClassVar[int]
    stdio: MCPTransportStdio
    http: MCPTransportHTTP
    def __init__(self, stdio: _Optional[_Union[MCPTransportStdio, _Mapping]] = ..., http: _Optional[_Union[MCPTransportHTTP, _Mapping]] = ...) -> None: ...

class MCPTransportStdio(_message.Message):
    __slots__ = ("enabled", "profile")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    profile: str
    def __init__(self, enabled: _Optional[bool] = ..., profile: _Optional[str] = ...) -> None: ...

class MCPTransportHTTP(_message.Message):
    __slots__ = ("enabled", "addr", "profile", "session_timeout_seconds")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    ADDR_FIELD_NUMBER: _ClassVar[int]
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    SESSION_TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    addr: str
    profile: str
    session_timeout_seconds: int
    def __init__(self, enabled: _Optional[bool] = ..., addr: _Optional[str] = ..., profile: _Optional[str] = ..., session_timeout_seconds: _Optional[int] = ...) -> None: ...
