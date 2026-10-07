from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateBackendRequest(_message.Message):
    __slots__ = ("backend_id", "backend")
    BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    backend_id: str
    backend: _types_pb2.StorageBackend
    def __init__(self, backend_id: _Optional[str] = ..., backend: _Optional[_Union[_types_pb2.StorageBackend, _Mapping]] = ...) -> None: ...

class GetBackendRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateBackendRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "backend")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    backend: _types_pb2.StorageBackend
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., backend: _Optional[_Union[_types_pb2.StorageBackend, _Mapping]] = ...) -> None: ...

class DeleteBackendRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class DeleteBackendResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListBackendsRequest(_message.Message):
    __slots__ = ("page", "filter")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ...) -> None: ...

class ListBackendsResponse(_message.Message):
    __slots__ = ("backends", "page")
    BACKENDS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    backends: _containers.RepeatedCompositeFieldContainer[_types_pb2.StorageBackend]
    page: _pagination_pb2.PageResponse
    def __init__(self, backends: _Optional[_Iterable[_Union[_types_pb2.StorageBackend, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class RotateCredentialsRequest(_message.Message):
    __slots__ = ("name", "new_secret_ref", "grace_period")
    NAME_FIELD_NUMBER: _ClassVar[int]
    NEW_SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    GRACE_PERIOD_FIELD_NUMBER: _ClassVar[int]
    name: str
    new_secret_ref: str
    grace_period: str
    def __init__(self, name: _Optional[str] = ..., new_secret_ref: _Optional[str] = ..., grace_period: _Optional[str] = ...) -> None: ...

class TestBackendRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class TestBackendResponse(_message.Message):
    __slots__ = ("reachable", "error_message", "latency_ms", "features", "compatibility")
    REACHABLE_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    LATENCY_MS_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    COMPATIBILITY_FIELD_NUMBER: _ClassVar[int]
    reachable: bool
    error_message: str
    latency_ms: int
    features: _containers.RepeatedCompositeFieldContainer[_types_pb2.StorageFeatureSupport]
    compatibility: _types_pb2.StorageCompatibility
    def __init__(self, reachable: _Optional[bool] = ..., error_message: _Optional[str] = ..., latency_ms: _Optional[int] = ..., features: _Optional[_Iterable[_Union[_types_pb2.StorageFeatureSupport, _Mapping]]] = ..., compatibility: _Optional[_Union[_types_pb2.StorageCompatibility, str]] = ...) -> None: ...

class SetBackendEnabledRequest(_message.Message):
    __slots__ = ("name", "enabled", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    enabled: bool
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., enabled: _Optional[bool] = ..., resource_version: _Optional[str] = ...) -> None: ...

class SetBackendReadOnlyRequest(_message.Message):
    __slots__ = ("name", "read_only", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    read_only: bool
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., read_only: _Optional[bool] = ..., resource_version: _Optional[str] = ...) -> None: ...

class SetBackendMaintenanceRequest(_message.Message):
    __slots__ = ("name", "maintenance", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    MAINTENANCE_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    maintenance: bool
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., maintenance: _Optional[bool] = ..., resource_version: _Optional[str] = ...) -> None: ...
