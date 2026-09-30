import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import scope_pb2 as _scope_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class User(_message.Message):
    __slots__ = ("name", "user_id", "tenant_id", "subject", "display_name", "roles", "scopes", "disabled", "resource_version", "created_at", "updated_at", "last_login_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_LOGIN_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    user_id: str
    tenant_id: str
    subject: str
    display_name: str
    roles: _containers.RepeatedScalarFieldContainer[str]
    scopes: _containers.RepeatedCompositeFieldContainer[_scope_pb2.Scope]
    disabled: bool
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    last_login_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., user_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., subject: _Optional[str] = ..., display_name: _Optional[str] = ..., roles: _Optional[_Iterable[str]] = ..., scopes: _Optional[_Iterable[_Union[_scope_pb2.Scope, _Mapping]]] = ..., disabled: _Optional[bool] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_login_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class TokenPair(_message.Message):
    __slots__ = ("access_token", "access_expires_in_seconds", "refresh_token", "refresh_expires_in_seconds", "token_type", "audience")
    ACCESS_TOKEN_FIELD_NUMBER: _ClassVar[int]
    ACCESS_EXPIRES_IN_SECONDS_FIELD_NUMBER: _ClassVar[int]
    REFRESH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    REFRESH_EXPIRES_IN_SECONDS_FIELD_NUMBER: _ClassVar[int]
    TOKEN_TYPE_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    access_token: str
    access_expires_in_seconds: int
    refresh_token: str
    refresh_expires_in_seconds: int
    token_type: str
    audience: str
    def __init__(self, access_token: _Optional[str] = ..., access_expires_in_seconds: _Optional[int] = ..., refresh_token: _Optional[str] = ..., refresh_expires_in_seconds: _Optional[int] = ..., token_type: _Optional[str] = ..., audience: _Optional[str] = ...) -> None: ...
