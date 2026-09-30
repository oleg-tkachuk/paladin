import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class UserSettings(_message.Message):
    __slots__ = ("name", "user_id", "tenant_id", "timezone", "locale", "theme", "preferences", "resource_version", "created_at", "updated_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    TIMEZONE_FIELD_NUMBER: _ClassVar[int]
    LOCALE_FIELD_NUMBER: _ClassVar[int]
    THEME_FIELD_NUMBER: _ClassVar[int]
    PREFERENCES_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    user_id: str
    tenant_id: str
    timezone: str
    locale: str
    theme: str
    preferences: _struct_pb2.Struct
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., user_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., timezone: _Optional[str] = ..., locale: _Optional[str] = ..., theme: _Optional[str] = ..., preferences: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class GetMineRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class UpdateMineRequest(_message.Message):
    __slots__ = ("update_mask", "timezone", "locale", "theme", "preferences")
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    TIMEZONE_FIELD_NUMBER: _ClassVar[int]
    LOCALE_FIELD_NUMBER: _ClassVar[int]
    THEME_FIELD_NUMBER: _ClassVar[int]
    PREFERENCES_FIELD_NUMBER: _ClassVar[int]
    update_mask: _field_mask_pb2.FieldMask
    timezone: str
    locale: str
    theme: str
    preferences: _struct_pb2.Struct
    def __init__(self, update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., timezone: _Optional[str] = ..., locale: _Optional[str] = ..., theme: _Optional[str] = ..., preferences: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class GetForUserRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class ListByTenantRequest(_message.Message):
    __slots__ = ("parent", "page")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListByTenantResponse(_message.Message):
    __slots__ = ("settings", "page")
    SETTINGS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    settings: _containers.RepeatedCompositeFieldContainer[UserSettings]
    page: _pagination_pb2.PageResponse
    def __init__(self, settings: _Optional[_Iterable[_Union[UserSettings, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class DeleteForUserRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class DeleteForUserResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
