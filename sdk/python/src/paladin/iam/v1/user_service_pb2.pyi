import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.common.v1 import scope_pb2 as _scope_pb2
from paladin.iam.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateUserRequest(_message.Message):
    __slots__ = ("parent", "subject", "display_name", "initial_password", "roles", "scopes")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    INITIAL_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    parent: str
    subject: str
    display_name: str
    initial_password: str
    roles: _containers.RepeatedScalarFieldContainer[str]
    scopes: _containers.RepeatedCompositeFieldContainer[_scope_pb2.Scope]
    def __init__(self, parent: _Optional[str] = ..., subject: _Optional[str] = ..., display_name: _Optional[str] = ..., initial_password: _Optional[str] = ..., roles: _Optional[_Iterable[str]] = ..., scopes: _Optional[_Iterable[_Union[_scope_pb2.Scope, _Mapping]]] = ...) -> None: ...

class GetUserRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateUserRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "display_name", "disabled", "roles")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    display_name: str
    disabled: bool
    roles: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., display_name: _Optional[str] = ..., disabled: _Optional[bool] = ..., roles: _Optional[_Iterable[str]] = ...) -> None: ...

class DeleteUserRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class DeleteUserResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListUsersRequest(_message.Message):
    __slots__ = ("parent", "page", "filter")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    filter: str
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ...) -> None: ...

class ListUsersResponse(_message.Message):
    __slots__ = ("users", "page")
    USERS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    users: _containers.RepeatedCompositeFieldContainer[_types_pb2.User]
    page: _pagination_pb2.PageResponse
    def __init__(self, users: _Optional[_Iterable[_Union[_types_pb2.User, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class GrantScopesRequest(_message.Message):
    __slots__ = ("name", "scopes")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    name: str
    scopes: _containers.RepeatedCompositeFieldContainer[_scope_pb2.Scope]
    def __init__(self, name: _Optional[str] = ..., scopes: _Optional[_Iterable[_Union[_scope_pb2.Scope, _Mapping]]] = ...) -> None: ...

class RevokeScopesRequest(_message.Message):
    __slots__ = ("name", "scopes")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    name: str
    scopes: _containers.RepeatedCompositeFieldContainer[_scope_pb2.Scope]
    def __init__(self, name: _Optional[str] = ..., scopes: _Optional[_Iterable[_Union[_scope_pb2.Scope, _Mapping]]] = ...) -> None: ...

class ResetPasswordRequest(_message.Message):
    __slots__ = ("name", "new_password")
    NAME_FIELD_NUMBER: _ClassVar[int]
    NEW_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    name: str
    new_password: str
    def __init__(self, name: _Optional[str] = ..., new_password: _Optional[str] = ...) -> None: ...

class ResetPasswordResponse(_message.Message):
    __slots__ = ("generated_password",)
    GENERATED_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    generated_password: str
    def __init__(self, generated_password: _Optional[str] = ...) -> None: ...

class ListOAuthGrantsRequest(_message.Message):
    __slots__ = ("name", "include_inactive")
    NAME_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_INACTIVE_FIELD_NUMBER: _ClassVar[int]
    name: str
    include_inactive: bool
    def __init__(self, name: _Optional[str] = ..., include_inactive: _Optional[bool] = ...) -> None: ...

class ListOAuthGrantsResponse(_message.Message):
    __slots__ = ("grants",)
    GRANTS_FIELD_NUMBER: _ClassVar[int]
    grants: _containers.RepeatedCompositeFieldContainer[OAuthGrant]
    def __init__(self, grants: _Optional[_Iterable[_Union[OAuthGrant, _Mapping]]] = ...) -> None: ...

class OAuthGrant(_message.Message):
    __slots__ = ("id", "client_id", "audience", "scopes", "granted_at", "refreshed_at", "expires_at", "state")
    class State(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        STATE_UNSPECIFIED: _ClassVar[OAuthGrant.State]
        STATE_ACTIVE: _ClassVar[OAuthGrant.State]
        STATE_REVOKED: _ClassVar[OAuthGrant.State]
        STATE_EXPIRED: _ClassVar[OAuthGrant.State]
    STATE_UNSPECIFIED: OAuthGrant.State
    STATE_ACTIVE: OAuthGrant.State
    STATE_REVOKED: OAuthGrant.State
    STATE_EXPIRED: OAuthGrant.State
    ID_FIELD_NUMBER: _ClassVar[int]
    CLIENT_ID_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    SCOPES_FIELD_NUMBER: _ClassVar[int]
    GRANTED_AT_FIELD_NUMBER: _ClassVar[int]
    REFRESHED_AT_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    id: str
    client_id: str
    audience: str
    scopes: _containers.RepeatedScalarFieldContainer[str]
    granted_at: _timestamp_pb2.Timestamp
    refreshed_at: _timestamp_pb2.Timestamp
    expires_at: _timestamp_pb2.Timestamp
    state: OAuthGrant.State
    def __init__(self, id: _Optional[str] = ..., client_id: _Optional[str] = ..., audience: _Optional[str] = ..., scopes: _Optional[_Iterable[str]] = ..., granted_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., refreshed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., expires_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., state: _Optional[_Union[OAuthGrant.State, str]] = ...) -> None: ...

class RevokeOAuthGrantRequest(_message.Message):
    __slots__ = ("name", "grant_id")
    NAME_FIELD_NUMBER: _ClassVar[int]
    GRANT_ID_FIELD_NUMBER: _ClassVar[int]
    name: str
    grant_id: str
    def __init__(self, name: _Optional[str] = ..., grant_id: _Optional[str] = ...) -> None: ...

class RevokeOAuthGrantResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
