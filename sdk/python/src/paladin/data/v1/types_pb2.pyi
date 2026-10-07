import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TaintSignal(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TAINT_SIGNAL_UNSPECIFIED: _ClassVar[TaintSignal]
    TAINT_SIGNAL_PROMPT_INJECTION: _ClassVar[TaintSignal]
    TAINT_SIGNAL_PII: _ClassVar[TaintSignal]
    TAINT_SIGNAL_SECRETS: _ClassVar[TaintSignal]

class ObjectState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OBJECT_STATE_UNSPECIFIED: _ClassVar[ObjectState]
    OBJECT_STATE_PENDING: _ClassVar[ObjectState]
    OBJECT_STATE_AVAILABLE: _ClassVar[ObjectState]
    OBJECT_STATE_FAILED: _ClassVar[ObjectState]
    OBJECT_STATE_DELETED: _ClassVar[ObjectState]
TAINT_SIGNAL_UNSPECIFIED: TaintSignal
TAINT_SIGNAL_PROMPT_INJECTION: TaintSignal
TAINT_SIGNAL_PII: TaintSignal
TAINT_SIGNAL_SECRETS: TaintSignal
OBJECT_STATE_UNSPECIFIED: ObjectState
OBJECT_STATE_PENDING: ObjectState
OBJECT_STATE_AVAILABLE: ObjectState
OBJECT_STATE_FAILED: ObjectState
OBJECT_STATE_DELETED: ObjectState

class Object(_message.Message):
    __slots__ = ("name", "object_id", "tenant_id", "collection", "key", "state", "content_type", "size_bytes", "etag", "checksum", "sequencer", "metadata", "tags", "external_ref", "resource_version", "created_at", "updated_at", "committed_at", "terminated_at", "presign_expires_at", "lock", "placement", "taint")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    NAME_FIELD_NUMBER: _ClassVar[int]
    OBJECT_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    COLLECTION_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    ETAG_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_FIELD_NUMBER: _ClassVar[int]
    SEQUENCER_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_REF_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    COMMITTED_AT_FIELD_NUMBER: _ClassVar[int]
    TERMINATED_AT_FIELD_NUMBER: _ClassVar[int]
    PRESIGN_EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    LOCK_FIELD_NUMBER: _ClassVar[int]
    PLACEMENT_FIELD_NUMBER: _ClassVar[int]
    TAINT_FIELD_NUMBER: _ClassVar[int]
    name: str
    object_id: str
    tenant_id: str
    collection: str
    key: str
    state: ObjectState
    content_type: str
    size_bytes: int
    etag: str
    checksum: ChecksumDigest
    sequencer: str
    metadata: _containers.ScalarMap[str, str]
    tags: _containers.ScalarMap[str, str]
    external_ref: str
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    committed_at: _timestamp_pb2.Timestamp
    terminated_at: _timestamp_pb2.Timestamp
    presign_expires_at: _timestamp_pb2.Timestamp
    lock: ObjectLockState
    placement: PhysicalPlacement
    taint: _containers.RepeatedScalarFieldContainer[TaintSignal]
    def __init__(self, name: _Optional[str] = ..., object_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., collection: _Optional[str] = ..., key: _Optional[str] = ..., state: _Optional[_Union[ObjectState, str]] = ..., content_type: _Optional[str] = ..., size_bytes: _Optional[int] = ..., etag: _Optional[str] = ..., checksum: _Optional[_Union[ChecksumDigest, _Mapping]] = ..., sequencer: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ..., tags: _Optional[_Mapping[str, str]] = ..., external_ref: _Optional[str] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., committed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., terminated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., presign_expires_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., lock: _Optional[_Union[ObjectLockState, _Mapping]] = ..., placement: _Optional[_Union[PhysicalPlacement, _Mapping]] = ..., taint: _Optional[_Iterable[_Union[TaintSignal, str]]] = ...) -> None: ...

class ChecksumDigest(_message.Message):
    __slots__ = ("algorithm", "value", "part_size_bytes")
    ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    PART_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    algorithm: str
    value: str
    part_size_bytes: int
    def __init__(self, algorithm: _Optional[str] = ..., value: _Optional[str] = ..., part_size_bytes: _Optional[int] = ...) -> None: ...

class ObjectLockState(_message.Message):
    __slots__ = ("mode", "retain_until", "legal_hold")
    MODE_FIELD_NUMBER: _ClassVar[int]
    RETAIN_UNTIL_FIELD_NUMBER: _ClassVar[int]
    LEGAL_HOLD_FIELD_NUMBER: _ClassVar[int]
    mode: str
    retain_until: _timestamp_pb2.Timestamp
    legal_hold: bool
    def __init__(self, mode: _Optional[str] = ..., retain_until: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., legal_hold: _Optional[bool] = ...) -> None: ...

class PhysicalPlacement(_message.Message):
    __slots__ = ("backend_id", "bucket_id", "storage_path")
    BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    BUCKET_ID_FIELD_NUMBER: _ClassVar[int]
    STORAGE_PATH_FIELD_NUMBER: _ClassVar[int]
    backend_id: str
    bucket_id: str
    storage_path: str
    def __init__(self, backend_id: _Optional[str] = ..., bucket_id: _Optional[str] = ..., storage_path: _Optional[str] = ...) -> None: ...

class CompletedPart(_message.Message):
    __slots__ = ("part_number", "etag", "checksum_value")
    PART_NUMBER_FIELD_NUMBER: _ClassVar[int]
    ETAG_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_VALUE_FIELD_NUMBER: _ClassVar[int]
    part_number: int
    etag: str
    checksum_value: str
    def __init__(self, part_number: _Optional[int] = ..., etag: _Optional[str] = ..., checksum_value: _Optional[str] = ...) -> None: ...

class PartInfo(_message.Message):
    __slots__ = ("part_number", "size_bytes", "etag", "uploaded_at")
    PART_NUMBER_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    ETAG_FIELD_NUMBER: _ClassVar[int]
    UPLOADED_AT_FIELD_NUMBER: _ClassVar[int]
    part_number: int
    size_bytes: int
    etag: str
    uploaded_at: _timestamp_pb2.Timestamp
    def __init__(self, part_number: _Optional[int] = ..., size_bytes: _Optional[int] = ..., etag: _Optional[str] = ..., uploaded_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
