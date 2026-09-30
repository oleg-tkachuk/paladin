from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.common.v1 import scope_pb2 as _scope_pb2
from paladin.iam.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
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
