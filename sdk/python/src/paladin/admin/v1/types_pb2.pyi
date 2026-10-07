import datetime

from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import resource_pb2 as _resource_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class StorageFeature(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STORAGE_FEATURE_UNSPECIFIED: _ClassVar[StorageFeature]
    STORAGE_FEATURE_CONDITIONAL_PUT: _ClassVar[StorageFeature]
    STORAGE_FEATURE_CHECKSUM_SHA256: _ClassVar[StorageFeature]
    STORAGE_FEATURE_MULTIPART_UPLOAD: _ClassVar[StorageFeature]
    STORAGE_FEATURE_SERVER_SIDE_COPY: _ClassVar[StorageFeature]
    STORAGE_FEATURE_PRESIGNED_POST: _ClassVar[StorageFeature]
    STORAGE_FEATURE_BUCKET_CREATE: _ClassVar[StorageFeature]
    STORAGE_FEATURE_ANONYMOUS_READ_POLICY: _ClassVar[StorageFeature]

class FeatureSupport(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FEATURE_SUPPORT_UNSPECIFIED: _ClassVar[FeatureSupport]
    FEATURE_SUPPORT_SUPPORTED: _ClassVar[FeatureSupport]
    FEATURE_SUPPORT_UNSUPPORTED: _ClassVar[FeatureSupport]
    FEATURE_SUPPORT_UNKNOWN: _ClassVar[FeatureSupport]

class StorageCompatibility(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STORAGE_COMPATIBILITY_UNSPECIFIED: _ClassVar[StorageCompatibility]
    STORAGE_COMPATIBILITY_UNVERIFIED: _ClassVar[StorageCompatibility]
    STORAGE_COMPATIBILITY_COMPATIBLE: _ClassVar[StorageCompatibility]
    STORAGE_COMPATIBILITY_INCOMPATIBLE: _ClassVar[StorageCompatibility]

class StorageKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STORAGE_KIND_UNSPECIFIED: _ClassVar[StorageKind]
    STORAGE_KIND_AWS_S3: _ClassVar[StorageKind]
    STORAGE_KIND_S3_COMPATIBLE: _ClassVar[StorageKind]
    STORAGE_KIND_GCS: _ClassVar[StorageKind]

class SseType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SSE_TYPE_UNSPECIFIED: _ClassVar[SseType]
    SSE_TYPE_NONE: _ClassVar[SseType]
    SSE_TYPE_AES256: _ClassVar[SseType]
    SSE_TYPE_KMS: _ClassVar[SseType]

class EventTarget(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_TARGET_UNSPECIFIED: _ClassVar[EventTarget]
    EVENT_TARGET_NONE: _ClassVar[EventTarget]
    EVENT_TARGET_SQS: _ClassVar[EventTarget]
    EVENT_TARGET_REDIS: _ClassVar[EventTarget]

class ObjectLockMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OBJECT_LOCK_MODE_UNSPECIFIED: _ClassVar[ObjectLockMode]
    OBJECT_LOCK_MODE_GOVERNANCE: _ClassVar[ObjectLockMode]
    OBJECT_LOCK_MODE_COMPLIANCE: _ClassVar[ObjectLockMode]
STORAGE_FEATURE_UNSPECIFIED: StorageFeature
STORAGE_FEATURE_CONDITIONAL_PUT: StorageFeature
STORAGE_FEATURE_CHECKSUM_SHA256: StorageFeature
STORAGE_FEATURE_MULTIPART_UPLOAD: StorageFeature
STORAGE_FEATURE_SERVER_SIDE_COPY: StorageFeature
STORAGE_FEATURE_PRESIGNED_POST: StorageFeature
STORAGE_FEATURE_BUCKET_CREATE: StorageFeature
STORAGE_FEATURE_ANONYMOUS_READ_POLICY: StorageFeature
FEATURE_SUPPORT_UNSPECIFIED: FeatureSupport
FEATURE_SUPPORT_SUPPORTED: FeatureSupport
FEATURE_SUPPORT_UNSUPPORTED: FeatureSupport
FEATURE_SUPPORT_UNKNOWN: FeatureSupport
STORAGE_COMPATIBILITY_UNSPECIFIED: StorageCompatibility
STORAGE_COMPATIBILITY_UNVERIFIED: StorageCompatibility
STORAGE_COMPATIBILITY_COMPATIBLE: StorageCompatibility
STORAGE_COMPATIBILITY_INCOMPATIBLE: StorageCompatibility
STORAGE_KIND_UNSPECIFIED: StorageKind
STORAGE_KIND_AWS_S3: StorageKind
STORAGE_KIND_S3_COMPATIBLE: StorageKind
STORAGE_KIND_GCS: StorageKind
SSE_TYPE_UNSPECIFIED: SseType
SSE_TYPE_NONE: SseType
SSE_TYPE_AES256: SseType
SSE_TYPE_KMS: SseType
EVENT_TARGET_UNSPECIFIED: EventTarget
EVENT_TARGET_NONE: EventTarget
EVENT_TARGET_SQS: EventTarget
EVENT_TARGET_REDIS: EventTarget
OBJECT_LOCK_MODE_UNSPECIFIED: ObjectLockMode
OBJECT_LOCK_MODE_GOVERNANCE: ObjectLockMode
OBJECT_LOCK_MODE_COMPLIANCE: ObjectLockMode

class StorageBackend(_message.Message):
    __slots__ = ("name", "backend_id", "display_name", "kind", "endpoint", "public_endpoint", "region", "force_path_style", "credentials_secret_ref", "sse", "events", "cedar_policy", "resource_version", "created_at", "updated_at", "enabled", "previous_credentials_secret_ref", "previous_credentials_valid_until", "read_only", "health_status", "health_message", "health_checked_at", "maintenance", "provider", "features", "compatibility")
    NAME_FIELD_NUMBER: _ClassVar[int]
    BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    ENDPOINT_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_ENDPOINT_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    FORCE_PATH_STYLE_FIELD_NUMBER: _ClassVar[int]
    CREDENTIALS_SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    SSE_FIELD_NUMBER: _ClassVar[int]
    EVENTS_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_CREDENTIALS_SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_CREDENTIALS_VALID_UNTIL_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    HEALTH_STATUS_FIELD_NUMBER: _ClassVar[int]
    HEALTH_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    HEALTH_CHECKED_AT_FIELD_NUMBER: _ClassVar[int]
    MAINTENANCE_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    COMPATIBILITY_FIELD_NUMBER: _ClassVar[int]
    name: str
    backend_id: str
    display_name: str
    kind: StorageKind
    endpoint: str
    public_endpoint: str
    region: str
    force_path_style: bool
    credentials_secret_ref: str
    sse: ServerSideEncryption
    events: EventSourceConfig
    cedar_policy: str
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    enabled: bool
    previous_credentials_secret_ref: str
    previous_credentials_valid_until: _timestamp_pb2.Timestamp
    read_only: bool
    health_status: str
    health_message: str
    health_checked_at: _timestamp_pb2.Timestamp
    maintenance: bool
    provider: str
    features: _containers.RepeatedCompositeFieldContainer[StorageFeatureSupport]
    compatibility: StorageCompatibility
    def __init__(self, name: _Optional[str] = ..., backend_id: _Optional[str] = ..., display_name: _Optional[str] = ..., kind: _Optional[_Union[StorageKind, str]] = ..., endpoint: _Optional[str] = ..., public_endpoint: _Optional[str] = ..., region: _Optional[str] = ..., force_path_style: _Optional[bool] = ..., credentials_secret_ref: _Optional[str] = ..., sse: _Optional[_Union[ServerSideEncryption, _Mapping]] = ..., events: _Optional[_Union[EventSourceConfig, _Mapping]] = ..., cedar_policy: _Optional[str] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., enabled: _Optional[bool] = ..., previous_credentials_secret_ref: _Optional[str] = ..., previous_credentials_valid_until: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., read_only: _Optional[bool] = ..., health_status: _Optional[str] = ..., health_message: _Optional[str] = ..., health_checked_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., maintenance: _Optional[bool] = ..., provider: _Optional[str] = ..., features: _Optional[_Iterable[_Union[StorageFeatureSupport, _Mapping]]] = ..., compatibility: _Optional[_Union[StorageCompatibility, str]] = ...) -> None: ...

class StorageFeatureSupport(_message.Message):
    __slots__ = ("feature", "support", "required", "enables", "message", "checked_at")
    FEATURE_FIELD_NUMBER: _ClassVar[int]
    SUPPORT_FIELD_NUMBER: _ClassVar[int]
    REQUIRED_FIELD_NUMBER: _ClassVar[int]
    ENABLES_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CHECKED_AT_FIELD_NUMBER: _ClassVar[int]
    feature: StorageFeature
    support: FeatureSupport
    required: bool
    enables: str
    message: str
    checked_at: _timestamp_pb2.Timestamp
    def __init__(self, feature: _Optional[_Union[StorageFeature, str]] = ..., support: _Optional[_Union[FeatureSupport, str]] = ..., required: _Optional[bool] = ..., enables: _Optional[str] = ..., message: _Optional[str] = ..., checked_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ServerSideEncryption(_message.Message):
    __slots__ = ("type", "key_id")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    KEY_ID_FIELD_NUMBER: _ClassVar[int]
    type: SseType
    key_id: str
    def __init__(self, type: _Optional[_Union[SseType, str]] = ..., key_id: _Optional[str] = ...) -> None: ...

class EventSourceConfig(_message.Message):
    __slots__ = ("enabled", "target", "queue_url", "poll_interval")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    QUEUE_URL_FIELD_NUMBER: _ClassVar[int]
    POLL_INTERVAL_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    target: EventTarget
    queue_url: str
    poll_interval: _duration_pb2.Duration
    def __init__(self, enabled: _Optional[bool] = ..., target: _Optional[_Union[EventTarget, str]] = ..., queue_url: _Optional[str] = ..., poll_interval: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class Bucket(_message.Message):
    __slots__ = ("name", "backend_id", "bucket_id", "display_name", "region", "owner_tenant_id", "cedar_policy", "constraints", "lifecycle_rules", "object_lock", "versioning", "replication", "labels", "resource_version", "created_at", "updated_at", "provision_state")
    class LabelsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    NAME_FIELD_NUMBER: _ClassVar[int]
    BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    BUCKET_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    OWNER_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    CONSTRAINTS_FIELD_NUMBER: _ClassVar[int]
    LIFECYCLE_RULES_FIELD_NUMBER: _ClassVar[int]
    OBJECT_LOCK_FIELD_NUMBER: _ClassVar[int]
    VERSIONING_FIELD_NUMBER: _ClassVar[int]
    REPLICATION_FIELD_NUMBER: _ClassVar[int]
    LABELS_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    PROVISION_STATE_FIELD_NUMBER: _ClassVar[int]
    name: str
    backend_id: str
    bucket_id: str
    display_name: str
    region: str
    owner_tenant_id: str
    cedar_policy: str
    constraints: BucketConstraints
    lifecycle_rules: _containers.RepeatedCompositeFieldContainer[LifecycleRule]
    object_lock: ObjectLockConfig
    versioning: BucketVersioning
    replication: BucketReplication
    labels: _containers.ScalarMap[str, str]
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    provision_state: str
    def __init__(self, name: _Optional[str] = ..., backend_id: _Optional[str] = ..., bucket_id: _Optional[str] = ..., display_name: _Optional[str] = ..., region: _Optional[str] = ..., owner_tenant_id: _Optional[str] = ..., cedar_policy: _Optional[str] = ..., constraints: _Optional[_Union[BucketConstraints, _Mapping]] = ..., lifecycle_rules: _Optional[_Iterable[_Union[LifecycleRule, _Mapping]]] = ..., object_lock: _Optional[_Union[ObjectLockConfig, _Mapping]] = ..., versioning: _Optional[_Union[BucketVersioning, _Mapping]] = ..., replication: _Optional[_Union[BucketReplication, _Mapping]] = ..., labels: _Optional[_Mapping[str, str]] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., provision_state: _Optional[str] = ...) -> None: ...

class BucketConstraints(_message.Message):
    __slots__ = ("max_object_size_bytes", "min_part_size_bytes", "max_part_size_bytes", "max_parts", "allowed_content_types", "max_presign_put_ttl", "max_presign_get_ttl", "required_checksum_algorithm")
    MAX_OBJECT_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    MIN_PART_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    MAX_PART_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    MAX_PARTS_FIELD_NUMBER: _ClassVar[int]
    ALLOWED_CONTENT_TYPES_FIELD_NUMBER: _ClassVar[int]
    MAX_PRESIGN_PUT_TTL_FIELD_NUMBER: _ClassVar[int]
    MAX_PRESIGN_GET_TTL_FIELD_NUMBER: _ClassVar[int]
    REQUIRED_CHECKSUM_ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    max_object_size_bytes: int
    min_part_size_bytes: int
    max_part_size_bytes: int
    max_parts: int
    allowed_content_types: _containers.RepeatedScalarFieldContainer[str]
    max_presign_put_ttl: _duration_pb2.Duration
    max_presign_get_ttl: _duration_pb2.Duration
    required_checksum_algorithm: _resource_pb2.ChecksumAlgorithm
    def __init__(self, max_object_size_bytes: _Optional[int] = ..., min_part_size_bytes: _Optional[int] = ..., max_part_size_bytes: _Optional[int] = ..., max_parts: _Optional[int] = ..., allowed_content_types: _Optional[_Iterable[str]] = ..., max_presign_put_ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., max_presign_get_ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., required_checksum_algorithm: _Optional[_Union[_resource_pb2.ChecksumAlgorithm, str]] = ...) -> None: ...

class LifecycleRule(_message.Message):
    __slots__ = ("id", "enabled", "match", "transition", "expiration")
    ID_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    MATCH_FIELD_NUMBER: _ClassVar[int]
    TRANSITION_FIELD_NUMBER: _ClassVar[int]
    EXPIRATION_FIELD_NUMBER: _ClassVar[int]
    id: str
    enabled: bool
    match: str
    transition: LifecycleTransition
    expiration: LifecycleExpiration
    def __init__(self, id: _Optional[str] = ..., enabled: _Optional[bool] = ..., match: _Optional[str] = ..., transition: _Optional[_Union[LifecycleTransition, _Mapping]] = ..., expiration: _Optional[_Union[LifecycleExpiration, _Mapping]] = ...) -> None: ...

class LifecycleTransition(_message.Message):
    __slots__ = ("after", "storage_class")
    AFTER_FIELD_NUMBER: _ClassVar[int]
    STORAGE_CLASS_FIELD_NUMBER: _ClassVar[int]
    after: _duration_pb2.Duration
    storage_class: str
    def __init__(self, after: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., storage_class: _Optional[str] = ...) -> None: ...

class LifecycleExpiration(_message.Message):
    __slots__ = ("after",)
    AFTER_FIELD_NUMBER: _ClassVar[int]
    after: _duration_pb2.Duration
    def __init__(self, after: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class ObjectLockConfig(_message.Message):
    __slots__ = ("enabled", "default_mode", "default_retention")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_MODE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_RETENTION_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    default_mode: ObjectLockMode
    default_retention: _duration_pb2.Duration
    def __init__(self, enabled: _Optional[bool] = ..., default_mode: _Optional[_Union[ObjectLockMode, str]] = ..., default_retention: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class BucketVersioning(_message.Message):
    __slots__ = ("enabled", "keep_deletes_forever")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    KEEP_DELETES_FOREVER_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    keep_deletes_forever: bool
    def __init__(self, enabled: _Optional[bool] = ..., keep_deletes_forever: _Optional[bool] = ...) -> None: ...

class BucketReplication(_message.Message):
    __slots__ = ("enabled", "destination_bucket", "filter")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_BUCKET_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    destination_bucket: str
    filter: str
    def __init__(self, enabled: _Optional[bool] = ..., destination_bucket: _Optional[str] = ..., filter: _Optional[str] = ...) -> None: ...

class Tenant(_message.Message):
    __slots__ = ("name", "tenant_id", "display_name", "labels", "inherited_cedar_policy", "resource_version", "created_at", "updated_at", "slug", "deleted_at", "default_bucket", "storage_layout")
    class LabelsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    NAME_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    LABELS_FIELD_NUMBER: _ClassVar[int]
    INHERITED_CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    SLUG_FIELD_NUMBER: _ClassVar[int]
    DELETED_AT_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_BUCKET_FIELD_NUMBER: _ClassVar[int]
    STORAGE_LAYOUT_FIELD_NUMBER: _ClassVar[int]
    name: str
    tenant_id: str
    display_name: str
    labels: _containers.ScalarMap[str, str]
    inherited_cedar_policy: str
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    slug: str
    deleted_at: _timestamp_pb2.Timestamp
    default_bucket: str
    storage_layout: str
    def __init__(self, name: _Optional[str] = ..., tenant_id: _Optional[str] = ..., display_name: _Optional[str] = ..., labels: _Optional[_Mapping[str, str]] = ..., inherited_cedar_policy: _Optional[str] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., slug: _Optional[str] = ..., deleted_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., default_bucket: _Optional[str] = ..., storage_layout: _Optional[str] = ...) -> None: ...

class Collection(_message.Message):
    __slots__ = ("name", "tenant_id", "collection", "display_name", "bucket", "completion_mode", "cedar_policy", "constraints", "resource_version", "created_at", "updated_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    COLLECTION_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    COMPLETION_MODE_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    CONSTRAINTS_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    tenant_id: str
    collection: str
    display_name: str
    bucket: str
    completion_mode: _resource_pb2.CompletionMode
    cedar_policy: str
    constraints: BucketConstraints
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., tenant_id: _Optional[str] = ..., collection: _Optional[str] = ..., display_name: _Optional[str] = ..., bucket: _Optional[str] = ..., completion_mode: _Optional[_Union[_resource_pb2.CompletionMode, str]] = ..., cedar_policy: _Optional[str] = ..., constraints: _Optional[_Union[BucketConstraints, _Mapping]] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Quota(_message.Message):
    __slots__ = ("name", "max_total_bytes", "max_object_count", "max_bytes_per_day", "max_objects_per_day", "usage", "resource_version", "updated_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    MAX_TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    MAX_OBJECT_COUNT_FIELD_NUMBER: _ClassVar[int]
    MAX_BYTES_PER_DAY_FIELD_NUMBER: _ClassVar[int]
    MAX_OBJECTS_PER_DAY_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    max_total_bytes: int
    max_object_count: int
    max_bytes_per_day: int
    max_objects_per_day: int
    usage: QuotaUsage
    resource_version: str
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., max_total_bytes: _Optional[int] = ..., max_object_count: _Optional[int] = ..., max_bytes_per_day: _Optional[int] = ..., max_objects_per_day: _Optional[int] = ..., usage: _Optional[_Union[QuotaUsage, _Mapping]] = ..., resource_version: _Optional[str] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class QuotaUsage(_message.Message):
    __slots__ = ("total_bytes", "object_count", "bytes_today", "objects_today", "last_reset_at")
    TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    OBJECT_COUNT_FIELD_NUMBER: _ClassVar[int]
    BYTES_TODAY_FIELD_NUMBER: _ClassVar[int]
    OBJECTS_TODAY_FIELD_NUMBER: _ClassVar[int]
    LAST_RESET_AT_FIELD_NUMBER: _ClassVar[int]
    total_bytes: int
    object_count: int
    bytes_today: int
    objects_today: int
    last_reset_at: _timestamp_pb2.Timestamp
    def __init__(self, total_bytes: _Optional[int] = ..., object_count: _Optional[int] = ..., bytes_today: _Optional[int] = ..., objects_today: _Optional[int] = ..., last_reset_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class AuditLogEntry(_message.Message):
    __slots__ = ("entry_id", "at", "actor_subject", "actor_tenant_id", "actor_audience", "action", "resource_name", "request_id", "source_ip", "before_json", "after_json", "error_message", "capability_id")
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    AT_FIELD_NUMBER: _ClassVar[int]
    ACTOR_SUBJECT_FIELD_NUMBER: _ClassVar[int]
    ACTOR_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    ACTOR_AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_NAME_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_IP_FIELD_NUMBER: _ClassVar[int]
    BEFORE_JSON_FIELD_NUMBER: _ClassVar[int]
    AFTER_JSON_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CAPABILITY_ID_FIELD_NUMBER: _ClassVar[int]
    entry_id: str
    at: _timestamp_pb2.Timestamp
    actor_subject: str
    actor_tenant_id: str
    actor_audience: str
    action: str
    resource_name: str
    request_id: str
    source_ip: str
    before_json: bytes
    after_json: bytes
    error_message: str
    capability_id: str
    def __init__(self, entry_id: _Optional[str] = ..., at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., actor_subject: _Optional[str] = ..., actor_tenant_id: _Optional[str] = ..., actor_audience: _Optional[str] = ..., action: _Optional[str] = ..., resource_name: _Optional[str] = ..., request_id: _Optional[str] = ..., source_ip: _Optional[str] = ..., before_json: _Optional[bytes] = ..., after_json: _Optional[bytes] = ..., error_message: _Optional[str] = ..., capability_id: _Optional[str] = ...) -> None: ...

class EventSubscription(_message.Message):
    __slots__ = ("name", "tenant_id", "filter", "sink", "disabled", "resource_version", "created_at", "updated_at")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    SINK_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    name: str
    tenant_id: str
    filter: str
    sink: EventSink
    disabled: bool
    resource_version: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, name: _Optional[str] = ..., tenant_id: _Optional[str] = ..., filter: _Optional[str] = ..., sink: _Optional[_Union[EventSink, _Mapping]] = ..., disabled: _Optional[bool] = ..., resource_version: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class EventSink(_message.Message):
    __slots__ = ("http", "kafka", "sqs", "nats", "rabbitmq")
    HTTP_FIELD_NUMBER: _ClassVar[int]
    KAFKA_FIELD_NUMBER: _ClassVar[int]
    SQS_FIELD_NUMBER: _ClassVar[int]
    NATS_FIELD_NUMBER: _ClassVar[int]
    RABBITMQ_FIELD_NUMBER: _ClassVar[int]
    http: HttpSink
    kafka: KafkaSink
    sqs: SqsSink
    nats: NatsSink
    rabbitmq: RabbitMqSink
    def __init__(self, http: _Optional[_Union[HttpSink, _Mapping]] = ..., kafka: _Optional[_Union[KafkaSink, _Mapping]] = ..., sqs: _Optional[_Union[SqsSink, _Mapping]] = ..., nats: _Optional[_Union[NatsSink, _Mapping]] = ..., rabbitmq: _Optional[_Union[RabbitMqSink, _Mapping]] = ...) -> None: ...

class HttpSink(_message.Message):
    __slots__ = ("url", "signing_secret_ref", "max_attempts", "format")
    URL_FIELD_NUMBER: _ClassVar[int]
    SIGNING_SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    MAX_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    FORMAT_FIELD_NUMBER: _ClassVar[int]
    url: str
    signing_secret_ref: str
    max_attempts: int
    format: str
    def __init__(self, url: _Optional[str] = ..., signing_secret_ref: _Optional[str] = ..., max_attempts: _Optional[int] = ..., format: _Optional[str] = ...) -> None: ...

class KafkaSink(_message.Message):
    __slots__ = ("brokers", "topic", "sasl_mechanism", "sasl_username", "sasl_password", "tls_enabled", "tls_client_cert", "tls_client_key", "tls_ca_cert")
    BROKERS_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    SASL_MECHANISM_FIELD_NUMBER: _ClassVar[int]
    SASL_USERNAME_FIELD_NUMBER: _ClassVar[int]
    SASL_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    TLS_ENABLED_FIELD_NUMBER: _ClassVar[int]
    TLS_CLIENT_CERT_FIELD_NUMBER: _ClassVar[int]
    TLS_CLIENT_KEY_FIELD_NUMBER: _ClassVar[int]
    TLS_CA_CERT_FIELD_NUMBER: _ClassVar[int]
    brokers: str
    topic: str
    sasl_mechanism: str
    sasl_username: str
    sasl_password: str
    tls_enabled: bool
    tls_client_cert: str
    tls_client_key: str
    tls_ca_cert: str
    def __init__(self, brokers: _Optional[str] = ..., topic: _Optional[str] = ..., sasl_mechanism: _Optional[str] = ..., sasl_username: _Optional[str] = ..., sasl_password: _Optional[str] = ..., tls_enabled: _Optional[bool] = ..., tls_client_cert: _Optional[str] = ..., tls_client_key: _Optional[str] = ..., tls_ca_cert: _Optional[str] = ...) -> None: ...

class SqsSink(_message.Message):
    __slots__ = ("queue_url", "region", "role_arn")
    QUEUE_URL_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    ROLE_ARN_FIELD_NUMBER: _ClassVar[int]
    queue_url: str
    region: str
    role_arn: str
    def __init__(self, queue_url: _Optional[str] = ..., region: _Optional[str] = ..., role_arn: _Optional[str] = ...) -> None: ...

class RabbitMqSink(_message.Message):
    __slots__ = ("url", "exchange", "routing_key", "tls_client_cert", "tls_client_key", "tls_ca_cert")
    URL_FIELD_NUMBER: _ClassVar[int]
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    ROUTING_KEY_FIELD_NUMBER: _ClassVar[int]
    TLS_CLIENT_CERT_FIELD_NUMBER: _ClassVar[int]
    TLS_CLIENT_KEY_FIELD_NUMBER: _ClassVar[int]
    TLS_CA_CERT_FIELD_NUMBER: _ClassVar[int]
    url: str
    exchange: str
    routing_key: str
    tls_client_cert: str
    tls_client_key: str
    tls_ca_cert: str
    def __init__(self, url: _Optional[str] = ..., exchange: _Optional[str] = ..., routing_key: _Optional[str] = ..., tls_client_cert: _Optional[str] = ..., tls_client_key: _Optional[str] = ..., tls_ca_cert: _Optional[str] = ...) -> None: ...

class NatsSink(_message.Message):
    __slots__ = ("url", "subject", "credentials_ref", "jetstream")
    URL_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    CREDENTIALS_REF_FIELD_NUMBER: _ClassVar[int]
    JETSTREAM_FIELD_NUMBER: _ClassVar[int]
    url: str
    subject: str
    credentials_ref: str
    jetstream: bool
    def __init__(self, url: _Optional[str] = ..., subject: _Optional[str] = ..., credentials_ref: _Optional[str] = ..., jetstream: _Optional[bool] = ...) -> None: ...
