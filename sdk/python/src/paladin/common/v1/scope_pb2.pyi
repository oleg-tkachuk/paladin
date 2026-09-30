from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ScopeType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SCOPE_TYPE_UNSPECIFIED: _ClassVar[ScopeType]
    SCOPE_TYPE_TENANT: _ClassVar[ScopeType]
    SCOPE_TYPE_BACKEND: _ClassVar[ScopeType]
    SCOPE_TYPE_BUCKET: _ClassVar[ScopeType]
    SCOPE_TYPE_OBJECT_KEY: _ClassVar[ScopeType]
SCOPE_TYPE_UNSPECIFIED: ScopeType
SCOPE_TYPE_TENANT: ScopeType
SCOPE_TYPE_BACKEND: ScopeType
SCOPE_TYPE_BUCKET: ScopeType
SCOPE_TYPE_OBJECT_KEY: ScopeType

class Scope(_message.Message):
    __slots__ = ("type", "value")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    type: ScopeType
    value: str
    def __init__(self, type: _Optional[_Union[ScopeType, str]] = ..., value: _Optional[str] = ...) -> None: ...
