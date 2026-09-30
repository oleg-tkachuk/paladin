from buf.validate import validate_pb2 as _validate_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.data.v1 import batch_service_pb2 as _batch_service_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetOperationRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class ListOperationsRequest(_message.Message):
    __slots__ = ("page", "filter", "sort_order")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    SORT_ORDER_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    sort_order: _pagination_pb2.SortOrder
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., sort_order: _Optional[_Union[_pagination_pb2.SortOrder, str]] = ...) -> None: ...

class ListOperationsResponse(_message.Message):
    __slots__ = ("operations", "page")
    OPERATIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    operations: _containers.RepeatedCompositeFieldContainer[_batch_service_pb2.Operation]
    page: _pagination_pb2.PageResponse
    def __init__(self, operations: _Optional[_Iterable[_Union[_batch_service_pb2.Operation, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class CancelOperationRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...
