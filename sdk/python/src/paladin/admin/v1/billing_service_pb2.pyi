import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.type import money_pb2 as _money_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetTenantSummaryRequest(_message.Message):
    __slots__ = ("tenant_id", "period_start", "period_end")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    def __init__(self, tenant_id: _Optional[str] = ..., period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class GetTenantSummaryResponse(_message.Message):
    __slots__ = ("total", "max_budget", "top_capabilities", "top_actors", "top_ops", "charge_count")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_FIELD_NUMBER: _ClassVar[int]
    TOP_CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    TOP_ACTORS_FIELD_NUMBER: _ClassVar[int]
    TOP_OPS_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    total: _money_pb2.Money
    max_budget: _money_pb2.Money
    top_capabilities: _containers.RepeatedCompositeFieldContainer[TopEntry]
    top_actors: _containers.RepeatedCompositeFieldContainer[TopEntry]
    top_ops: _containers.RepeatedCompositeFieldContainer[TopEntry]
    charge_count: int
    def __init__(self, total: _Optional[_Union[_money_pb2.Money, _Mapping]] = ..., max_budget: _Optional[_Union[_money_pb2.Money, _Mapping]] = ..., top_capabilities: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., top_actors: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., top_ops: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., charge_count: _Optional[int] = ...) -> None: ...

class TopEntry(_message.Message):
    __slots__ = ("label", "charge_count", "spent")
    LABEL_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    SPENT_FIELD_NUMBER: _ClassVar[int]
    label: str
    charge_count: int
    spent: _money_pb2.Money
    def __init__(self, label: _Optional[str] = ..., charge_count: _Optional[int] = ..., spent: _Optional[_Union[_money_pb2.Money, _Mapping]] = ...) -> None: ...

class GetTenantTimeSeriesRequest(_message.Message):
    __slots__ = ("tenant_id", "period_start", "period_end", "granularity")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    GRANULARITY_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    granularity: str
    def __init__(self, tenant_id: _Optional[str] = ..., period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., granularity: _Optional[str] = ...) -> None: ...

class GetTenantTimeSeriesResponse(_message.Message):
    __slots__ = ("buckets",)
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    buckets: _containers.RepeatedCompositeFieldContainer[TimeBucket]
    def __init__(self, buckets: _Optional[_Iterable[_Union[TimeBucket, _Mapping]]] = ...) -> None: ...

class TimeBucket(_message.Message):
    __slots__ = ("start", "charge_count", "spent")
    START_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    SPENT_FIELD_NUMBER: _ClassVar[int]
    start: _timestamp_pb2.Timestamp
    charge_count: int
    spent: _money_pb2.Money
    def __init__(self, start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., charge_count: _Optional[int] = ..., spent: _Optional[_Union[_money_pb2.Money, _Mapping]] = ...) -> None: ...
