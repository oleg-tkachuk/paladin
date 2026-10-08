import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TenantBudget(_message.Message):
    __slots__ = ("tenant_id", "resource_version", "period_start", "period_end", "updated_at", "unit_code", "max_budget_micros", "spent_micros")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    PERIOD_START_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    UNIT_CODE_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_MICROS_FIELD_NUMBER: _ClassVar[int]
    SPENT_MICROS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    resource_version: str
    period_start: _timestamp_pb2.Timestamp
    period_end: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    unit_code: str
    max_budget_micros: int
    spent_micros: int
    def __init__(self, tenant_id: _Optional[str] = ..., resource_version: _Optional[str] = ..., period_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., unit_code: _Optional[str] = ..., max_budget_micros: _Optional[int] = ..., spent_micros: _Optional[int] = ...) -> None: ...

class TenantBudgetServiceGetRequest(_message.Message):
    __slots__ = ("tenant_id",)
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    def __init__(self, tenant_id: _Optional[str] = ...) -> None: ...

class TenantBudgetServiceGetResponse(_message.Message):
    __slots__ = ("budget",)
    BUDGET_FIELD_NUMBER: _ClassVar[int]
    budget: TenantBudget
    def __init__(self, budget: _Optional[_Union[TenantBudget, _Mapping]] = ...) -> None: ...

class TenantBudgetServiceSetRequest(_message.Message):
    __slots__ = ("tenant_id", "resource_version", "reset_spend", "period_end", "unit_code", "max_budget_micros")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    RESET_SPEND_FIELD_NUMBER: _ClassVar[int]
    PERIOD_END_FIELD_NUMBER: _ClassVar[int]
    UNIT_CODE_FIELD_NUMBER: _ClassVar[int]
    MAX_BUDGET_MICROS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    resource_version: str
    reset_spend: bool
    period_end: _timestamp_pb2.Timestamp
    unit_code: str
    max_budget_micros: int
    def __init__(self, tenant_id: _Optional[str] = ..., resource_version: _Optional[str] = ..., reset_spend: _Optional[bool] = ..., period_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., unit_code: _Optional[str] = ..., max_budget_micros: _Optional[int] = ...) -> None: ...

class TenantBudgetServiceSetResponse(_message.Message):
    __slots__ = ("budget",)
    BUDGET_FIELD_NUMBER: _ClassVar[int]
    budget: TenantBudget
    def __init__(self, budget: _Optional[_Union[TenantBudget, _Mapping]] = ...) -> None: ...

class TenantBudgetSummary(_message.Message):
    __slots__ = ("tenant_id", "slug", "display_name", "budget", "utilisation_pct")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SLUG_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    BUDGET_FIELD_NUMBER: _ClassVar[int]
    UTILISATION_PCT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    slug: str
    display_name: str
    budget: TenantBudget
    utilisation_pct: float
    def __init__(self, tenant_id: _Optional[str] = ..., slug: _Optional[str] = ..., display_name: _Optional[str] = ..., budget: _Optional[_Union[TenantBudget, _Mapping]] = ..., utilisation_pct: _Optional[float] = ...) -> None: ...

class TenantBudgetServiceSummarizeRequest(_message.Message):
    __slots__ = ("threshold_pct", "unlimited_only", "exclude_inactive", "limit", "page_token")
    THRESHOLD_PCT_FIELD_NUMBER: _ClassVar[int]
    UNLIMITED_ONLY_FIELD_NUMBER: _ClassVar[int]
    EXCLUDE_INACTIVE_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    threshold_pct: float
    unlimited_only: bool
    exclude_inactive: bool
    limit: int
    page_token: str
    def __init__(self, threshold_pct: _Optional[float] = ..., unlimited_only: _Optional[bool] = ..., exclude_inactive: _Optional[bool] = ..., limit: _Optional[int] = ..., page_token: _Optional[str] = ...) -> None: ...

class TenantBudgetServiceSummarizeResponse(_message.Message):
    __slots__ = ("summaries", "next_page_token")
    SUMMARIES_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    summaries: _containers.RepeatedCompositeFieldContainer[TenantBudgetSummary]
    next_page_token: str
    def __init__(self, summaries: _Optional[_Iterable[_Union[TenantBudgetSummary, _Mapping]]] = ..., next_page_token: _Optional[str] = ...) -> None: ...
