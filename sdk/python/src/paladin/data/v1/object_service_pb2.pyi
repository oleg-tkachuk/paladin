import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.common.v1 import resource_pb2 as _resource_pb2
from paladin.data.v1 import types_pb2 as _types_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PresignTransport(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PRESIGN_TRANSPORT_UNSPECIFIED: _ClassVar[PresignTransport]
    PRESIGN_TRANSPORT_PUT: _ClassVar[PresignTransport]
    PRESIGN_TRANSPORT_POST: _ClassVar[PresignTransport]
PRESIGN_TRANSPORT_UNSPECIFIED: PresignTransport
PRESIGN_TRANSPORT_PUT: PresignTransport
PRESIGN_TRANSPORT_POST: PresignTransport

class ObjectVersion(_message.Message):
    __slots__ = ("name", "version_id", "object_id", "is_delete_marker", "storage_path", "size_bytes", "etag", "checksum", "content_type", "metadata", "tags", "lock", "created_at", "is_current")
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
    VERSION_ID_FIELD_NUMBER: _ClassVar[int]
    OBJECT_ID_FIELD_NUMBER: _ClassVar[int]
    IS_DELETE_MARKER_FIELD_NUMBER: _ClassVar[int]
    STORAGE_PATH_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    ETAG_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    LOCK_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    IS_CURRENT_FIELD_NUMBER: _ClassVar[int]
    name: str
    version_id: str
    object_id: str
    is_delete_marker: bool
    storage_path: str
    size_bytes: int
    etag: str
    checksum: _types_pb2.ChecksumDigest
    content_type: str
    metadata: _containers.ScalarMap[str, str]
    tags: _containers.ScalarMap[str, str]
    lock: _types_pb2.ObjectLockState
    created_at: _timestamp_pb2.Timestamp
    is_current: bool
    def __init__(self, name: _Optional[str] = ..., version_id: _Optional[str] = ..., object_id: _Optional[str] = ..., is_delete_marker: _Optional[bool] = ..., storage_path: _Optional[str] = ..., size_bytes: _Optional[int] = ..., etag: _Optional[str] = ..., checksum: _Optional[_Union[_types_pb2.ChecksumDigest, _Mapping]] = ..., content_type: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ..., tags: _Optional[_Mapping[str, str]] = ..., lock: _Optional[_Union[_types_pb2.ObjectLockState, _Mapping]] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., is_current: _Optional[bool] = ...) -> None: ...

class ListObjectVersionsRequest(_message.Message):
    __slots__ = ("parent", "page")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListObjectVersionsResponse(_message.Message):
    __slots__ = ("versions", "page")
    VERSIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    versions: _containers.RepeatedCompositeFieldContainer[ObjectVersion]
    page: _pagination_pb2.PageResponse
    def __init__(self, versions: _Optional[_Iterable[_Union[ObjectVersion, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class GetObjectVersionRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class RestoreObjectVersionRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class UploadObjectRequest(_message.Message):
    __slots__ = ("parent", "key", "content_type", "size_hint_bytes", "checksum_algorithm", "metadata", "tags", "external_ref", "transport", "idempotency_key", "checksum_value")
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
    SIZE_HINT_BYTES_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_REF_FIELD_NUMBER: _ClassVar[int]
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_VALUE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    key: str
    content_type: str
    size_hint_bytes: int
    checksum_algorithm: _resource_pb2.ChecksumAlgorithm
    metadata: _containers.ScalarMap[str, str]
    tags: _containers.ScalarMap[str, str]
    external_ref: str
    transport: PresignTransport
    idempotency_key: str
    checksum_value: str
    def __init__(self, parent: _Optional[str] = ..., key: _Optional[str] = ..., content_type: _Optional[str] = ..., size_hint_bytes: _Optional[int] = ..., checksum_algorithm: _Optional[_Union[_resource_pb2.ChecksumAlgorithm, str]] = ..., metadata: _Optional[_Mapping[str, str]] = ..., tags: _Optional[_Mapping[str, str]] = ..., external_ref: _Optional[str] = ..., transport: _Optional[_Union[PresignTransport, str]] = ..., idempotency_key: _Optional[str] = ..., checksum_value: _Optional[str] = ...) -> None: ...

class UploadObjectResponse(_message.Message):
    __slots__ = ("object", "upload_url", "completion_mode")
    OBJECT_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    COMPLETION_MODE_FIELD_NUMBER: _ClassVar[int]
    object: _types_pb2.Object
    upload_url: _resource_pb2.PresignedUrl
    completion_mode: _resource_pb2.CompletionMode
    def __init__(self, object: _Optional[_Union[_types_pb2.Object, _Mapping]] = ..., upload_url: _Optional[_Union[_resource_pb2.PresignedUrl, _Mapping]] = ..., completion_mode: _Optional[_Union[_resource_pb2.CompletionMode, str]] = ...) -> None: ...

class DownloadObjectRequest(_message.Message):
    __slots__ = ("name", "ttl", "content_disposition", "require_etag_match")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TTL_FIELD_NUMBER: _ClassVar[int]
    CONTENT_DISPOSITION_FIELD_NUMBER: _ClassVar[int]
    REQUIRE_ETAG_MATCH_FIELD_NUMBER: _ClassVar[int]
    name: str
    ttl: _duration_pb2.Duration
    content_disposition: str
    require_etag_match: bool
    def __init__(self, name: _Optional[str] = ..., ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., content_disposition: _Optional[str] = ..., require_etag_match: _Optional[bool] = ...) -> None: ...

class DownloadObjectResponse(_message.Message):
    __slots__ = ("object", "download_url")
    OBJECT_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    object: _types_pb2.Object
    download_url: _resource_pb2.PresignedUrl
    def __init__(self, object: _Optional[_Union[_types_pb2.Object, _Mapping]] = ..., download_url: _Optional[_Union[_resource_pb2.PresignedUrl, _Mapping]] = ...) -> None: ...

class GetObjectRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class LookupObjectRequest(_message.Message):
    __slots__ = ("parent", "key")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    parent: str
    key: str
    def __init__(self, parent: _Optional[str] = ..., key: _Optional[str] = ...) -> None: ...

class UpdateObjectRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "metadata", "tags", "content_type", "external_ref")
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
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_REF_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    metadata: _containers.ScalarMap[str, str]
    tags: _containers.ScalarMap[str, str]
    content_type: str
    external_ref: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., metadata: _Optional[_Mapping[str, str]] = ..., tags: _Optional[_Mapping[str, str]] = ..., content_type: _Optional[str] = ..., external_ref: _Optional[str] = ...) -> None: ...

class CompleteObjectRequest(_message.Message):
    __slots__ = ("name", "etag", "checksum_value")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ETAG_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_VALUE_FIELD_NUMBER: _ClassVar[int]
    name: str
    etag: str
    checksum_value: str
    def __init__(self, name: _Optional[str] = ..., etag: _Optional[str] = ..., checksum_value: _Optional[str] = ...) -> None: ...

class DeleteObjectRequest(_message.Message):
    __slots__ = ("name", "resource_version", "permanent", "bypass_governance_retention")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    PERMANENT_FIELD_NUMBER: _ClassVar[int]
    BYPASS_GOVERNANCE_RETENTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    permanent: bool
    bypass_governance_retention: bool
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., permanent: _Optional[bool] = ..., bypass_governance_retention: _Optional[bool] = ...) -> None: ...

class DeleteObjectResponse(_message.Message):
    __slots__ = ("object",)
    OBJECT_FIELD_NUMBER: _ClassVar[int]
    object: _types_pb2.Object
    def __init__(self, object: _Optional[_Union[_types_pb2.Object, _Mapping]] = ...) -> None: ...

class SetObjectRetentionRequest(_message.Message):
    __slots__ = ("name", "mode", "retain_until", "bypass_governance_retention")
    NAME_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    RETAIN_UNTIL_FIELD_NUMBER: _ClassVar[int]
    BYPASS_GOVERNANCE_RETENTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    mode: str
    retain_until: _timestamp_pb2.Timestamp
    bypass_governance_retention: bool
    def __init__(self, name: _Optional[str] = ..., mode: _Optional[str] = ..., retain_until: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., bypass_governance_retention: _Optional[bool] = ...) -> None: ...

class SetObjectLegalHoldRequest(_message.Message):
    __slots__ = ("name", "legal_hold")
    NAME_FIELD_NUMBER: _ClassVar[int]
    LEGAL_HOLD_FIELD_NUMBER: _ClassVar[int]
    name: str
    legal_hold: bool
    def __init__(self, name: _Optional[str] = ..., legal_hold: _Optional[bool] = ...) -> None: ...

class SetObjectTaintRequest(_message.Message):
    __slots__ = ("name", "signals")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SIGNALS_FIELD_NUMBER: _ClassVar[int]
    name: str
    signals: _containers.RepeatedScalarFieldContainer[_types_pb2.TaintSignal]
    def __init__(self, name: _Optional[str] = ..., signals: _Optional[_Iterable[_Union[_types_pb2.TaintSignal, str]]] = ...) -> None: ...

class GetObjectLockRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class RestoreObjectRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class CopyObjectRequest(_message.Message):
    __slots__ = ("source_name", "destination_collection", "destination_key", "metadata_override", "tags_override")
    SOURCE_NAME_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_COLLECTION_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_KEY_FIELD_NUMBER: _ClassVar[int]
    METADATA_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    TAGS_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    source_name: str
    destination_collection: str
    destination_key: str
    metadata_override: MetadataOverride
    tags_override: TagsOverride
    def __init__(self, source_name: _Optional[str] = ..., destination_collection: _Optional[str] = ..., destination_key: _Optional[str] = ..., metadata_override: _Optional[_Union[MetadataOverride, _Mapping]] = ..., tags_override: _Optional[_Union[TagsOverride, _Mapping]] = ...) -> None: ...

class MetadataOverride(_message.Message):
    __slots__ = ("metadata",)
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    METADATA_FIELD_NUMBER: _ClassVar[int]
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class TagsOverride(_message.Message):
    __slots__ = ("tags",)
    class TagsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TAGS_FIELD_NUMBER: _ClassVar[int]
    tags: _containers.ScalarMap[str, str]
    def __init__(self, tags: _Optional[_Mapping[str, str]] = ...) -> None: ...

class ListObjectsRequest(_message.Message):
    __slots__ = ("parent", "page", "filter", "order_by", "sort_order")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    ORDER_BY_FIELD_NUMBER: _ClassVar[int]
    SORT_ORDER_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    filter: str
    order_by: str
    sort_order: _pagination_pb2.SortOrder
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., order_by: _Optional[str] = ..., sort_order: _Optional[_Union[_pagination_pb2.SortOrder, str]] = ...) -> None: ...

class ListObjectsResponse(_message.Message):
    __slots__ = ("objects", "page")
    OBJECTS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    objects: _containers.RepeatedCompositeFieldContainer[_types_pb2.Object]
    page: _pagination_pb2.PageResponse
    def __init__(self, objects: _Optional[_Iterable[_Union[_types_pb2.Object, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class CountObjectsRequest(_message.Message):
    __slots__ = ("parent", "filter")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    parent: str
    filter: str
    def __init__(self, parent: _Optional[str] = ..., filter: _Optional[str] = ...) -> None: ...

class CountObjectsResponse(_message.Message):
    __slots__ = ("approximate_count", "exact")
    APPROXIMATE_COUNT_FIELD_NUMBER: _ClassVar[int]
    EXACT_FIELD_NUMBER: _ClassVar[int]
    approximate_count: int
    exact: bool
    def __init__(self, approximate_count: _Optional[int] = ..., exact: _Optional[bool] = ...) -> None: ...
