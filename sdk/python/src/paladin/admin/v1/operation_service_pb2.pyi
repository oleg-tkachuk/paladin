import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import any_pb2 as _any_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.rpc import status_pb2 as _status_pb2
from paladin.common.v1 import operation_pb2 as _operation_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Operation(_message.Message):
    __slots__ = ("name", "type", "metadata", "done", "error", "response", "initiator_subject", "initiator_tenant_id", "created_at", "updated_at", "done_at", "state", "target", "dismissed_at", "expire_time", "failed_count")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    INITIATOR_SUBJECT_FIELD_NUMBER: _ClassVar[int]
    INITIATOR_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    DONE_AT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    DISMISSED_AT_FIELD_NUMBER: _ClassVar[int]
    EXPIRE_TIME_FIELD_NUMBER: _ClassVar[int]
    FAILED_COUNT_FIELD_NUMBER: _ClassVar[int]
    name: str
    type: str
    metadata: _any_pb2.Any
    done: bool
    error: _status_pb2.Status
    response: _any_pb2.Any
    initiator_subject: str
    initiator_tenant_id: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    done_at: _timestamp_pb2.Timestamp
    state: _operation_pb2.OperationState
    target: _operation_pb2.OperationTarget
    dismissed_at: _timestamp_pb2.Timestamp
    expire_time: _timestamp_pb2.Timestamp
    failed_count: int
    def __init__(self, name: _Optional[str] = ..., type: _Optional[str] = ..., metadata: _Optional[_Union[_any_pb2.Any, _Mapping]] = ..., done: _Optional[bool] = ..., error: _Optional[_Union[_status_pb2.Status, _Mapping]] = ..., response: _Optional[_Union[_any_pb2.Any, _Mapping]] = ..., initiator_subject: _Optional[str] = ..., initiator_tenant_id: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., done_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., state: _Optional[_Union[_operation_pb2.OperationState, str]] = ..., target: _Optional[_Union[_operation_pb2.OperationTarget, _Mapping]] = ..., dismissed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., expire_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., failed_count: _Optional[int] = ...) -> None: ...

class GetOperationRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class ListOperationsRequest(_message.Message):
    __slots__ = ("page", "filter", "sort_order", "mine", "include_dismissed")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    SORT_ORDER_FIELD_NUMBER: _ClassVar[int]
    MINE_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_DISMISSED_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    sort_order: _pagination_pb2.SortOrder
    mine: bool
    include_dismissed: bool
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., sort_order: _Optional[_Union[_pagination_pb2.SortOrder, str]] = ..., mine: _Optional[bool] = ..., include_dismissed: _Optional[bool] = ...) -> None: ...

class ListOperationsResponse(_message.Message):
    __slots__ = ("operations", "page")
    OPERATIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    operations: _containers.RepeatedCompositeFieldContainer[Operation]
    page: _pagination_pb2.PageResponse
    def __init__(self, operations: _Optional[_Iterable[_Union[Operation, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

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
