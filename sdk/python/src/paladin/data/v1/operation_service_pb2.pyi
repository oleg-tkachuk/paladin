from buf.validate import validate_pb2 as _validate_pb2
from paladin.common.v1 import facet_pb2 as _facet_pb2
from paladin.common.v1 import operation_pb2 as _operation_pb2
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
    __slots__ = ("page", "filter", "sort_order", "mine", "include_dismissed", "facets", "count_facets")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    SORT_ORDER_FIELD_NUMBER: _ClassVar[int]
    MINE_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_DISMISSED_FIELD_NUMBER: _ClassVar[int]
    FACETS_FIELD_NUMBER: _ClassVar[int]
    COUNT_FACETS_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    sort_order: _pagination_pb2.SortOrder
    mine: bool
    include_dismissed: bool
    facets: _operation_pb2.OperationFacets
    count_facets: bool
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., sort_order: _Optional[_Union[_pagination_pb2.SortOrder, str]] = ..., mine: _Optional[bool] = ..., include_dismissed: _Optional[bool] = ..., facets: _Optional[_Union[_operation_pb2.OperationFacets, _Mapping]] = ..., count_facets: _Optional[bool] = ...) -> None: ...

class ListOperationsResponse(_message.Message):
    __slots__ = ("operations", "page", "facet_counts")
    OPERATIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FACET_COUNTS_FIELD_NUMBER: _ClassVar[int]
    operations: _containers.RepeatedCompositeFieldContainer[_batch_service_pb2.Operation]
    page: _pagination_pb2.PageResponse
    facet_counts: _containers.RepeatedCompositeFieldContainer[_facet_pb2.FacetCount]
    def __init__(self, operations: _Optional[_Iterable[_Union[_batch_service_pb2.Operation, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ..., facet_counts: _Optional[_Iterable[_Union[_facet_pb2.FacetCount, _Mapping]]] = ...) -> None: ...

class CancelOperationRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class DismissOperationsRequest(_message.Message):
    __slots__ = ("names",)
    NAMES_FIELD_NUMBER: _ClassVar[int]
    names: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, names: _Optional[_Iterable[str]] = ...) -> None: ...

class DismissOperationsResponse(_message.Message):
    __slots__ = ("dismissed_count",)
    DISMISSED_COUNT_FIELD_NUMBER: _ClassVar[int]
    dismissed_count: int
    def __init__(self, dismissed_count: _Optional[int] = ...) -> None: ...
