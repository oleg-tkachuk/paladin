import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.type import money_pb2 as _money_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListTenantSpendRequest(_message.Message):
    __slots__ = ("period_start", "period_end", "limit")
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    limit: int
    def __init__(self, period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., limit: _Optional[int] = ...) -> None: ...

class ListTenantSpendResponse(_message.Message):
    __slots__ = ("tenants", "truncated")
    TENANTS_FIELD_NUMBER: _ClassVar[int]
    TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    tenants: _containers.RepeatedCompositeFieldContainer[TenantSpend]
    truncated: bool
    def __init__(self, tenants: _Optional[_Iterable[_Union[TenantSpend, _Mapping]]] = ..., truncated: _Optional[bool] = ...) -> None: ...

class TenantSpend(_message.Message):
    __slots__ = ("tenant_id", "slug", "display_name", "deleted", "spent", "charge_count")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SLUG_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    DELETED_FIELD_NUMBER: _ClassVar[int]
    SPENT_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    slug: str
    display_name: str
    deleted: bool
    spent: _money_pb2.Money
    charge_count: int
    def __init__(self, tenant_id: _Optional[str] = ..., slug: _Optional[str] = ..., display_name: _Optional[str] = ..., deleted: _Optional[bool] = ..., spent: _Optional[_Union[_money_pb2.Money, _Mapping]] = ..., charge_count: _Optional[int] = ...) -> None: ...

class GetBillingSettingsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetBillingSettingsResponse(_message.Message):
    __slots__ = ("currency_code",)
    CURRENCY_CODE_FIELD_NUMBER: _ClassVar[int]
    currency_code: str
    def __init__(self, currency_code: _Optional[str] = ...) -> None: ...

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
    __slots__ = ("total", "max_budget", "unlimited_budget", "top_capabilities", "top_actors", "top_ops", "charge_count")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_FIELD_NUMBER: _ClassVar[int]
    UNLIMITED_BUDGET_FIELD_NUMBER: _ClassVar[int]
    TOP_CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    TOP_ACTORS_FIELD_NUMBER: _ClassVar[int]
    TOP_OPS_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    total: _money_pb2.Money
    max_budget: _money_pb2.Money
    unlimited_budget: _types_pb2.UnlimitedBudget
    top_capabilities: _containers.RepeatedCompositeFieldContainer[TopEntry]
    top_actors: _containers.RepeatedCompositeFieldContainer[TopEntry]
    top_ops: _containers.RepeatedCompositeFieldContainer[TopEntry]
    charge_count: int
    def __init__(self, total: _Optional[_Union[_money_pb2.Money, _Mapping]] = ..., max_budget: _Optional[_Union[_money_pb2.Money, _Mapping]] = ..., unlimited_budget: _Optional[_Union[_types_pb2.UnlimitedBudget, _Mapping]] = ..., top_capabilities: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., top_actors: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., top_ops: _Optional[_Iterable[_Union[TopEntry, _Mapping]]] = ..., charge_count: _Optional[int] = ...) -> None: ...

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

class GetPlatformTimeSeriesRequest(_message.Message):
    __slots__ = ("period_start", "period_end", "granularity")
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    GRANULARITY_FIELD_NUMBER: _ClassVar[int]
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    granularity: str
    def __init__(self, period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., granularity: _Optional[str] = ...) -> None: ...

class GetPlatformTimeSeriesResponse(_message.Message):
    __slots__ = ("buckets",)
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    buckets: _containers.RepeatedCompositeFieldContainer[TimeBucket]
    def __init__(self, buckets: _Optional[_Iterable[_Union[TimeBucket, _Mapping]]] = ...) -> None: ...

class ListTenantTimeSeriesRequest(_message.Message):
    __slots__ = ("tenant_ids", "period_start", "period_end", "granularity")
    TENANT_IDS_FIELD_NUMBER: _ClassVar[int]
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    GRANULARITY_FIELD_NUMBER: _ClassVar[int]
    tenant_ids: _containers.RepeatedScalarFieldContainer[str]
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    granularity: str
    def __init__(self, tenant_ids: _Optional[_Iterable[str]] = ..., period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., granularity: _Optional[str] = ...) -> None: ...

class ListTenantTimeSeriesResponse(_message.Message):
    __slots__ = ("series",)
    SERIES_FIELD_NUMBER: _ClassVar[int]
    series: _containers.RepeatedCompositeFieldContainer[TenantTimeSeries]
    def __init__(self, series: _Optional[_Iterable[_Union[TenantTimeSeries, _Mapping]]] = ...) -> None: ...

class TenantTimeSeries(_message.Message):
    __slots__ = ("tenant_id", "buckets")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    buckets: _containers.RepeatedCompositeFieldContainer[TimeBucket]
    def __init__(self, tenant_id: _Optional[str] = ..., buckets: _Optional[_Iterable[_Union[TimeBucket, _Mapping]]] = ...) -> None: ...

class TimeBucket(_message.Message):
    __slots__ = ("start", "charge_count", "spent")
    START_FIELD_NUMBER: _ClassVar[int]
    CHARGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    SPENT_FIELD_NUMBER: _ClassVar[int]
    start: _timestamp_pb2.Timestamp
    charge_count: int
    spent: _money_pb2.Money
    def __init__(self, start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., charge_count: _Optional[int] = ..., spent: _Optional[_Union[_money_pb2.Money, _Mapping]] = ...) -> None: ...
