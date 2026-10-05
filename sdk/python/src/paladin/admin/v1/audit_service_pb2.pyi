from buf.validate import validate_pb2 as _validate_pb2
from paladin.admin.v1 import operation_service_pb2 as _operation_service_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListAuditLogRequest(_message.Message):
    __slots__ = ("page", "filter", "tenant_id")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    tenant_id: str
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., tenant_id: _Optional[str] = ...) -> None: ...

class ListAuditLogResponse(_message.Message):
    __slots__ = ("entries", "page")
    ENTRIES_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    entries: _containers.RepeatedCompositeFieldContainer[_types_pb2.AuditLogEntry]
    page: _pagination_pb2.PageResponse
    def __init__(self, entries: _Optional[_Iterable[_Union[_types_pb2.AuditLogEntry, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

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
