import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ComponentStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    COMPONENT_STATUS_UNSPECIFIED: _ClassVar[ComponentStatus]
    COMPONENT_STATUS_HEALTHY: _ClassVar[ComponentStatus]
    COMPONENT_STATUS_DEGRADED: _ClassVar[ComponentStatus]
    COMPONENT_STATUS_UNHEALTHY: _ClassVar[ComponentStatus]
    COMPONENT_STATUS_DISABLED: _ClassVar[ComponentStatus]
COMPONENT_STATUS_UNSPECIFIED: ComponentStatus
COMPONENT_STATUS_HEALTHY: ComponentStatus
COMPONENT_STATUS_DEGRADED: ComponentStatus
COMPONENT_STATUS_UNHEALTHY: ComponentStatus
COMPONENT_STATUS_DISABLED: ComponentStatus

class GetVersionRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class VersionInfo(_message.Message):
    __slots__ = ("version", "commit", "build_time", "go_version")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    COMMIT_FIELD_NUMBER: _ClassVar[int]
    BUILD_TIME_FIELD_NUMBER: _ClassVar[int]
    GO_VERSION_FIELD_NUMBER: _ClassVar[int]
    version: str
    commit: str
    build_time: _timestamp_pb2.Timestamp
    go_version: str
    def __init__(self, version: _Optional[str] = ..., commit: _Optional[str] = ..., build_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., go_version: _Optional[str] = ...) -> None: ...

class GetHealthRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ComponentHealth(_message.Message):
    __slots__ = ("name", "status", "message", "latency_ms", "category", "critical")
    NAME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    LATENCY_MS_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    CRITICAL_FIELD_NUMBER: _ClassVar[int]
    name: str
    status: ComponentStatus
    message: str
    latency_ms: int
    category: str
    critical: bool
    def __init__(self, name: _Optional[str] = ..., status: _Optional[_Union[ComponentStatus, str]] = ..., message: _Optional[str] = ..., latency_ms: _Optional[int] = ..., category: _Optional[str] = ..., critical: _Optional[bool] = ...) -> None: ...

class HealthInfo(_message.Message):
    __slots__ = ("status", "components", "role")
    STATUS_FIELD_NUMBER: _ClassVar[int]
    COMPONENTS_FIELD_NUMBER: _ClassVar[int]
    ROLE_FIELD_NUMBER: _ClassVar[int]
    status: ComponentStatus
    components: _containers.RepeatedCompositeFieldContainer[ComponentHealth]
    role: str
    def __init__(self, status: _Optional[_Union[ComponentStatus, str]] = ..., components: _Optional[_Iterable[_Union[ComponentHealth, _Mapping]]] = ..., role: _Optional[str] = ...) -> None: ...
