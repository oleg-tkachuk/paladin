from buf.validate import validate_pb2 as _validate_pb2
from paladin.admin.v1 import operation_service_pb2 as _operation_service_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from paladin.common.v1 import facet_pb2 as _facet_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AuditOutcome(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    AUDIT_OUTCOME_UNSPECIFIED: _ClassVar[AuditOutcome]
    AUDIT_OUTCOME_OK: _ClassVar[AuditOutcome]
    AUDIT_OUTCOME_ERROR: _ClassVar[AuditOutcome]
AUDIT_OUTCOME_UNSPECIFIED: AuditOutcome
AUDIT_OUTCOME_OK: AuditOutcome
AUDIT_OUTCOME_ERROR: AuditOutcome

class ListAuditLogRequest(_message.Message):
    __slots__ = ("page", "filter", "tenant_id", "order_by", "sort_order", "facets", "count_facets")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_BY_FIELD_NUMBER: _ClassVar[int]
    SORT_ORDER_FIELD_NUMBER: _ClassVar[int]
    FACETS_FIELD_NUMBER: _ClassVar[int]
    COUNT_FACETS_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    tenant_id: str
    order_by: str
    sort_order: _pagination_pb2.SortOrder
    facets: AuditFacets
    count_facets: bool
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., tenant_id: _Optional[str] = ..., order_by: _Optional[str] = ..., sort_order: _Optional[_Union[_pagination_pb2.SortOrder, str]] = ..., facets: _Optional[_Union[AuditFacets, _Mapping]] = ..., count_facets: _Optional[bool] = ...) -> None: ...

class AuditFacets(_message.Message):
    __slots__ = ("planes", "outcomes", "services")
    PLANES_FIELD_NUMBER: _ClassVar[int]
    OUTCOMES_FIELD_NUMBER: _ClassVar[int]
    SERVICES_FIELD_NUMBER: _ClassVar[int]
    planes: _containers.RepeatedScalarFieldContainer[str]
    outcomes: _containers.RepeatedScalarFieldContainer[AuditOutcome]
    services: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, planes: _Optional[_Iterable[str]] = ..., outcomes: _Optional[_Iterable[_Union[AuditOutcome, str]]] = ..., services: _Optional[_Iterable[str]] = ...) -> None: ...

class ListAuditLogResponse(_message.Message):
    __slots__ = ("entries", "page", "facet_counts")
    ENTRIES_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FACET_COUNTS_FIELD_NUMBER: _ClassVar[int]
    entries: _containers.RepeatedCompositeFieldContainer[_types_pb2.AuditLogEntry]
    page: _pagination_pb2.PageResponse
    facet_counts: _containers.RepeatedCompositeFieldContainer[_facet_pb2.FacetCount]
    def __init__(self, entries: _Optional[_Iterable[_Union[_types_pb2.AuditLogEntry, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ..., facet_counts: _Optional[_Iterable[_Union[_facet_pb2.FacetCount, _Mapping]]] = ...) -> None: ...

class GetAuditLogEntryRequest(_message.Message):
    __slots__ = ("entry_id",)
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    entry_id: str
    def __init__(self, entry_id: _Optional[str] = ...) -> None: ...

class ExportAuditLogRequest(_message.Message):
    __slots__ = ("filter", "destination")
    FILTER_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_FIELD_NUMBER: _ClassVar[int]
    filter: str
    destination: str
    def __init__(self, filter: _Optional[str] = ..., destination: _Optional[str] = ...) -> None: ...
