import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import any_pb2 as _any_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.rpc import status_pb2 as _status_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Operation(_message.Message):
    __slots__ = ("name", "type", "metadata", "done", "error", "response", "created_at", "updated_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    type: str
    metadata: _any_pb2.Any
    done: bool
    error: _status_pb2.Status
    response: _any_pb2.Any
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., type: _Optional[str] = ..., metadata: _Optional[_Union[_any_pb2.Any, _Mapping]] = ..., done: _Optional[bool] = ..., error: _Optional[_Union[_status_pb2.Status, _Mapping]] = ..., response: _Optional[_Union[_any_pb2.Any, _Mapping]] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ObjectSelector(_message.Message):
    __slots__ = ("names", "filter")
    NAMES_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    names: _containers.RepeatedScalarFieldContainer[str]
    filter: str
    def __init__(self, names: _Optional[_Iterable[str]] = ..., filter: _Optional[str] = ...) -> None: ...

class BatchDeleteObjectsRequest(_message.Message):
    __slots__ = ("parent", "selector", "permanent")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    SELECTOR_FIELD_NUMBER: _ClassVar[int]
    PERMANENT_FIELD_NUMBER: _ClassVar[int]
    parent: str
    selector: ObjectSelector
    permanent: bool
    def __init__(self, parent: _Optional[str] = ..., selector: _Optional[_Union[ObjectSelector, _Mapping]] = ..., permanent: _Optional[bool] = ...) -> None: ...

class BatchCopyObjectsRequest(_message.Message):
    __slots__ = ("source_parent", "selector", "destination_collection", "destination_key_template")
    SOURCE_PARENT_FIELD_NUMBER: _ClassVar[int]
    SELECTOR_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_COLLECTION_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_KEY_TEMPLATE_FIELD_NUMBER: _ClassVar[int]
    source_parent: str
    selector: ObjectSelector
    destination_collection: str
    destination_key_template: str
    def __init__(self, source_parent: _Optional[str] = ..., selector: _Optional[_Union[ObjectSelector, _Mapping]] = ..., destination_collection: _Optional[str] = ..., destination_key_template: _Optional[str] = ...) -> None: ...

class BatchRestoreObjectsRequest(_message.Message):
    __slots__ = ("parent", "selector")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    SELECTOR_FIELD_NUMBER: _ClassVar[int]
    parent: str
    selector: ObjectSelector
    def __init__(self, parent: _Optional[str] = ..., selector: _Optional[_Union[ObjectSelector, _Mapping]] = ...) -> None: ...

class BatchUpdateTagsRequest(_message.Message):
    __slots__ = ("parent", "selector", "tags", "replace")
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    PARENT_FIELD_NUMBER: _ClassVar[int]
    SELECTOR_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    REPLACE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    selector: ObjectSelector
    tags: _containers.ScalarMap[str, str]
    replace: bool
    def __init__(self, parent: _Optional[str] = ..., selector: _Optional[_Union[ObjectSelector, _Mapping]] = ..., tags: _Optional[_Mapping[str, str]] = ..., replace: _Optional[bool] = ...) -> None: ...
