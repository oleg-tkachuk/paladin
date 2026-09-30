import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.common.v1 import resource_pb2 as _resource_pb2
from paladin.data.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class InitiateMultipartUploadRequest(_message.Message):
    __slots__ = ("parent", "key", "content_type", "size_bytes", "checksum_algorithm", "metadata", "tags", "external_ref", "idempotency_key")
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
    PARENT_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_REF_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    parent: str
    key: str
    content_type: str
    size_bytes: int
    checksum_algorithm: _resource_pb2.ChecksumAlgorithm
    metadata: _containers.ScalarMap[str, str]
    tags: _containers.ScalarMap[str, str]
    external_ref: str
    idempotency_key: str
    def __init__(self, parent: _Optional[str] = ..., key: _Optional[str] = ..., content_type: _Optional[str] = ..., size_bytes: _Optional[int] = ..., checksum_algorithm: _Optional[_Union[_resource_pb2.ChecksumAlgorithm, str]] = ..., metadata: _Optional[_Mapping[str, str]] = ..., tags: _Optional[_Mapping[str, str]] = ..., external_ref: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class InitiateMultipartUploadResponse(_message.Message):
    __slots__ = ("object", "upload_id", "recommended_part_size", "total_parts")
    OBJECT_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_ID_FIELD_NUMBER: _ClassVar[int]
    RECOMMENDED_PART_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOTAL_PARTS_FIELD_NUMBER: _ClassVar[int]
    object: _types_pb2.Object
    upload_id: str
    recommended_part_size: int
    total_parts: int
    def __init__(self, object: _Optional[_Union[_types_pb2.Object, _Mapping]] = ..., upload_id: _Optional[str] = ..., recommended_part_size: _Optional[int] = ..., total_parts: _Optional[int] = ...) -> None: ...

class PresignPartRequest(_message.Message):
    __slots__ = ("object_name", "upload_id", "part_number", "ttl")
    OBJECT_NAME_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_ID_FIELD_NUMBER: _ClassVar[int]
    PART_NUMBER_FIELD_NUMBER: _ClassVar[int]
    TTL_FIELD_NUMBER: _ClassVar[int]
    object_name: str
    upload_id: str
    part_number: int
    ttl: _duration_pb2.Duration
    def __init__(self, object_name: _Optional[str] = ..., upload_id: _Optional[str] = ..., part_number: _Optional[int] = ..., ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class PresignPartResponse(_message.Message):
    __slots__ = ("upload_url",)
    UPLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    upload_url: _resource_pb2.PresignedUrl
    def __init__(self, upload_url: _Optional[_Union[_resource_pb2.PresignedUrl, _Mapping]] = ...) -> None: ...

class CompleteMultipartUploadRequest(_message.Message):
    __slots__ = ("object_name", "upload_id", "parts")
    OBJECT_NAME_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_ID_FIELD_NUMBER: _ClassVar[int]
    PARTS_FIELD_NUMBER: _ClassVar[int]
    object_name: str
    upload_id: str
    parts: _containers.RepeatedCompositeFieldContainer[_types_pb2.CompletedPart]
    def __init__(self, object_name: _Optional[str] = ..., upload_id: _Optional[str] = ..., parts: _Optional[_Iterable[_Union[_types_pb2.CompletedPart, _Mapping]]] = ...) -> None: ...

class AbortMultipartUploadRequest(_message.Message):
    __slots__ = ("object_name", "upload_id")
    OBJECT_NAME_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_ID_FIELD_NUMBER: _ClassVar[int]
    object_name: str
    upload_id: str
    def __init__(self, object_name: _Optional[str] = ..., upload_id: _Optional[str] = ...) -> None: ...

class AbortMultipartUploadResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListPartsRequest(_message.Message):
    __slots__ = ("object_name", "upload_id", "page")
    OBJECT_NAME_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    object_name: str
    upload_id: str
    page: _pagination_pb2.PageRequest
    def __init__(self, object_name: _Optional[str] = ..., upload_id: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListPartsResponse(_message.Message):
    __slots__ = ("parts", "page")
    PARTS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    parts: _containers.RepeatedCompositeFieldContainer[_types_pb2.PartInfo]
    page: _pagination_pb2.PageResponse
    def __init__(self, parts: _Optional[_Iterable[_Union[_types_pb2.PartInfo, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...
