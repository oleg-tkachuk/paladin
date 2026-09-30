from buf.validate import validate_pb2 as _validate_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListDistinctTagsRequest(_message.Message):
    __slots__ = ("parent", "page")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListDistinctTagsResponse(_message.Message):
    __slots__ = ("tags", "page")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: TagValues
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[TagValues, _Mapping]] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.MessageMap[str, TagValues]
    page: _pagination_pb2.PageResponse
    def __init__(self, tags: _Optional[_Mapping[str, TagValues]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class TagValues(_message.Message):
    __slots__ = ("values", "truncated")
    VALUES_FIELD_NUMBER: _ClassVar[int]
    TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    values: _containers.RepeatedScalarFieldContainer[str]
    truncated: bool
    def __init__(self, values: _Optional[_Iterable[str]] = ..., truncated: _Optional[bool] = ...) -> None: ...

class GetObjectTagsRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class GetObjectTagsResponse(_message.Message):
    __slots__ = ("tags",)
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.ScalarMap[str, str]
    def __init__(self, tags: _Optional[_Mapping[str, str]] = ...) -> None: ...

class PutObjectTagsRequest(_message.Message):
    __slots__ = ("name", "resource_version", "tags")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    tags: _containers.ScalarMap[str, str]
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., tags: _Optional[_Mapping[str, str]] = ...) -> None: ...

class PutObjectTagsResponse(_message.Message):
    __slots__ = ("tags",)
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.ScalarMap[str, str]
    def __init__(self, tags: _Optional[_Mapping[str, str]] = ...) -> None: ...

class DeleteObjectTagsRequest(_message.Message):
    __slots__ = ("name", "resource_version", "keys")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    KEYS_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    keys: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., keys: _Optional[_Iterable[str]] = ...) -> None: ...

class DeleteObjectTagsResponse(_message.Message):
    __slots__ = ("tags",)
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.ScalarMap[str, str]
    def __init__(self, tags: _Optional[_Mapping[str, str]] = ...) -> None: ...
