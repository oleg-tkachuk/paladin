import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetConfigRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetConfigResponse(_message.Message):
    __slots__ = ("yaml", "source_path")
    YAML_FIELD_NUMBER: _ClassVar[int]
    SOURCE_PATH_FIELD_NUMBER: _ClassVar[int]
    yaml: str
    source_path: str
    def __init__(self, yaml: _Optional[str] = ..., source_path: _Optional[str] = ...) -> None: ...

class GetDispatcherStatsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetDispatcherStatsResponse(_message.Message):
    __slots__ = ("available", "pending", "failed", "oldest_pending_seconds", "subscriptions")
    AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    PENDING_FIELD_NUMBER: _ClassVar[int]
    FAILED_FIELD_NUMBER: _ClassVar[int]
    OLDEST_PENDING_SECONDS_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIPTIONS_FIELD_NUMBER: _ClassVar[int]
    available: bool
    pending: int
    failed: int
    oldest_pending_seconds: int
    subscriptions: _containers.RepeatedCompositeFieldContainer[SubscriptionDeliveryStat]
    def __init__(self, available: _Optional[bool] = ..., pending: _Optional[int] = ..., failed: _Optional[int] = ..., oldest_pending_seconds: _Optional[int] = ..., subscriptions: _Optional[_Iterable[_Union[SubscriptionDeliveryStat, _Mapping]]] = ...) -> None: ...

class SubscriptionDeliveryStat(_message.Message):
    __slots__ = ("subscription_id", "tenant_id", "pending", "failed", "last_error", "last_status_code", "last_attempt_at")
    SUBSCRIPTION_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PENDING_FIELD_NUMBER: _ClassVar[int]
    FAILED_FIELD_NUMBER: _ClassVar[int]
    LAST_ERROR_FIELD_NUMBER: _ClassVar[int]
    LAST_STATUS_CODE_FIELD_NUMBER: _ClassVar[int]
    LAST_ATTEMPT_AT_FIELD_NUMBER: _ClassVar[int]
    subscription_id: str
    tenant_id: str
    pending: int
    failed: int
    last_error: str
    last_status_code: int
    last_attempt_at: str
    def __init__(self, subscription_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., pending: _Optional[int] = ..., failed: _Optional[int] = ..., last_error: _Optional[str] = ..., last_status_code: _Optional[int] = ..., last_attempt_at: _Optional[str] = ...) -> None: ...

class GetPlatformStatsRequest(_message.Message):
    __slots__ = ("tenant_page",)
    TENANT_PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_page: _pagination_pb2.PageRequest
    def __init__(self, tenant_page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class GetPlatformStatsResponse(_message.Message):
    __slots__ = ("tenants", "backends", "buckets", "collections", "users", "collected_at", "rls")
    TENANTS_FIELD_NUMBER: _ClassVar[int]
    BACKENDS_FIELD_NUMBER: _ClassVar[int]
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    COLLECTIONS_FIELD_NUMBER: _ClassVar[int]
    USERS_FIELD_NUMBER: _ClassVar[int]
    COLLECTED_AT_FIELD_NUMBER: _ClassVar[int]
    RLS_FIELD_NUMBER: _ClassVar[int]
    tenants: TenantStats
    backends: BackendStats
    buckets: BucketStats
    collections: CollectionStats
    users: UserStats
    collected_at: _timestamp_pb2.Timestamp
    rls: RLSStats
    def __init__(self, tenants: _Optional[_Union[TenantStats, _Mapping]] = ..., backends: _Optional[_Union[BackendStats, _Mapping]] = ..., buckets: _Optional[_Union[BucketStats, _Mapping]] = ..., collections: _Optional[_Union[CollectionStats, _Mapping]] = ..., users: _Optional[_Union[UserStats, _Mapping]] = ..., collected_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., rls: _Optional[_Union[RLSStats, _Mapping]] = ...) -> None: ...

class RLSStats(_message.Message):
    __slots__ = ("available", "objects", "quotas", "capabilities", "api_tokens", "subscriptions")
    AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    OBJECTS_FIELD_NUMBER: _ClassVar[int]
    QUOTAS_FIELD_NUMBER: _ClassVar[int]
    CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    API_TOKENS_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIPTIONS_FIELD_NUMBER: _ClassVar[int]
    available: bool
    objects: ObjectStats
    quotas: QuotaStats
    capabilities: CapabilityStats
    api_tokens: APITokenStats
    subscriptions: SubscriptionStats
    def __init__(self, available: _Optional[bool] = ..., objects: _Optional[_Union[ObjectStats, _Mapping]] = ..., quotas: _Optional[_Union[QuotaStats, _Mapping]] = ..., capabilities: _Optional[_Union[CapabilityStats, _Mapping]] = ..., api_tokens: _Optional[_Union[APITokenStats, _Mapping]] = ..., subscriptions: _Optional[_Union[SubscriptionStats, _Mapping]] = ...) -> None: ...

class QuotaStats(_message.Message):
    __slots__ = ("total", "tenant_scoped", "bucket_scoped", "with_limits", "at_limit", "near_limit", "usage_object_count", "usage_total_bytes")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    TENANT_SCOPED_FIELD_NUMBER: _ClassVar[int]
    BUCKET_SCOPED_FIELD_NUMBER: _ClassVar[int]
    WITH_LIMITS_FIELD_NUMBER: _ClassVar[int]
    AT_LIMIT_FIELD_NUMBER: _ClassVar[int]
    NEAR_LIMIT_FIELD_NUMBER: _ClassVar[int]
    USAGE_OBJECT_COUNT_FIELD_NUMBER: _ClassVar[int]
    USAGE_TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    total: int
    tenant_scoped: int
    bucket_scoped: int
    with_limits: int
    at_limit: int
    near_limit: int
    usage_object_count: int
    usage_total_bytes: int
    def __init__(self, total: _Optional[int] = ..., tenant_scoped: _Optional[int] = ..., bucket_scoped: _Optional[int] = ..., with_limits: _Optional[int] = ..., at_limit: _Optional[int] = ..., near_limit: _Optional[int] = ..., usage_object_count: _Optional[int] = ..., usage_total_bytes: _Optional[int] = ...) -> None: ...

class CapabilityStats(_message.Message):
    __slots__ = ("total", "active", "expired", "revoked", "delegated", "expiring_soon", "by_principal_kind")
    class ByPrincipalKindEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    EXPIRED_FIELD_NUMBER: _ClassVar[int]
    REVOKED_FIELD_NUMBER: _ClassVar[int]
    DELEGATED_FIELD_NUMBER: _ClassVar[int]
    EXPIRING_SOON_FIELD_NUMBER: _ClassVar[int]
    BY_PRINCIPAL_KIND_FIELD_NUMBER: _ClassVar[int]
    total: int
    active: int
    expired: int
    revoked: int
    delegated: int
    expiring_soon: int
    by_principal_kind: _containers.ScalarMap[str, int]
    def __init__(self, total: _Optional[int] = ..., active: _Optional[int] = ..., expired: _Optional[int] = ..., revoked: _Optional[int] = ..., delegated: _Optional[int] = ..., expiring_soon: _Optional[int] = ..., by_principal_kind: _Optional[_Mapping[str, int]] = ...) -> None: ...

class APITokenStats(_message.Message):
    __slots__ = ("total", "active", "expired", "revoked", "expiring_soon", "never_used")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    EXPIRED_FIELD_NUMBER: _ClassVar[int]
    REVOKED_FIELD_NUMBER: _ClassVar[int]
    EXPIRING_SOON_FIELD_NUMBER: _ClassVar[int]
    NEVER_USED_FIELD_NUMBER: _ClassVar[int]
    total: int
    active: int
    expired: int
    revoked: int
    expiring_soon: int
    never_used: int
    def __init__(self, total: _Optional[int] = ..., active: _Optional[int] = ..., expired: _Optional[int] = ..., revoked: _Optional[int] = ..., expiring_soon: _Optional[int] = ..., never_used: _Optional[int] = ...) -> None: ...

class SubscriptionStats(_message.Message):
    __slots__ = ("total", "enabled", "disabled", "with_filter", "by_sink_kind")
    class BySinkKindEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    WITH_FILTER_FIELD_NUMBER: _ClassVar[int]
    BY_SINK_KIND_FIELD_NUMBER: _ClassVar[int]
    total: int
    enabled: int
    disabled: int
    with_filter: int
    by_sink_kind: _containers.ScalarMap[str, int]
    def __init__(self, total: _Optional[int] = ..., enabled: _Optional[int] = ..., disabled: _Optional[int] = ..., with_filter: _Optional[int] = ..., by_sink_kind: _Optional[_Mapping[str, int]] = ...) -> None: ...

class TenantStats(_message.Message):
    __slots__ = ("total", "active", "trashed", "shared_layout", "dedicated_layout", "without_default_binding")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    TRASHED_FIELD_NUMBER: _ClassVar[int]
    SHARED_LAYOUT_FIELD_NUMBER: _ClassVar[int]
    DEDICATED_LAYOUT_FIELD_NUMBER: _ClassVar[int]
    WITHOUT_DEFAULT_BINDING_FIELD_NUMBER: _ClassVar[int]
    total: int
    active: int
    trashed: int
    shared_layout: int
    dedicated_layout: int
    without_default_binding: int
    def __init__(self, total: _Optional[int] = ..., active: _Optional[int] = ..., trashed: _Optional[int] = ..., shared_layout: _Optional[int] = ..., dedicated_layout: _Optional[int] = ..., without_default_binding: _Optional[int] = ...) -> None: ...

class BackendStats(_message.Message):
    __slots__ = ("total", "enabled", "disabled", "read_only", "maintenance", "by_kind")
    class ByKindEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    MAINTENANCE_FIELD_NUMBER: _ClassVar[int]
    BY_KIND_FIELD_NUMBER: _ClassVar[int]
    total: int
    enabled: int
    disabled: int
    read_only: int
    maintenance: int
    by_kind: _containers.ScalarMap[str, int]
    def __init__(self, total: _Optional[int] = ..., enabled: _Optional[int] = ..., disabled: _Optional[int] = ..., read_only: _Optional[int] = ..., maintenance: _Optional[int] = ..., by_kind: _Optional[_Mapping[str, int]] = ...) -> None: ...

class BucketStats(_message.Message):
    __slots__ = ("total", "by_provision_state", "by_backend", "tenant_owned", "shared", "versioning_enabled", "object_lock_enabled", "replication_enabled")
    class ByProvisionStateEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    class ByBackendEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    BY_PROVISION_STATE_FIELD_NUMBER: _ClassVar[int]
    BY_BACKEND_FIELD_NUMBER: _ClassVar[int]
    TENANT_OWNED_FIELD_NUMBER: _ClassVar[int]
    SHARED_FIELD_NUMBER: _ClassVar[int]
    VERSIONING_ENABLED_FIELD_NUMBER: _ClassVar[int]
    OBJECT_LOCK_ENABLED_FIELD_NUMBER: _ClassVar[int]
    REPLICATION_ENABLED_FIELD_NUMBER: _ClassVar[int]
    total: int
    by_provision_state: _containers.ScalarMap[str, int]
    by_backend: _containers.ScalarMap[str, int]
    tenant_owned: int
    shared: int
    versioning_enabled: int
    object_lock_enabled: int
    replication_enabled: int
    def __init__(self, total: _Optional[int] = ..., by_provision_state: _Optional[_Mapping[str, int]] = ..., by_backend: _Optional[_Mapping[str, int]] = ..., tenant_owned: _Optional[int] = ..., shared: _Optional[int] = ..., versioning_enabled: _Optional[int] = ..., object_lock_enabled: _Optional[int] = ..., replication_enabled: _Optional[int] = ...) -> None: ...

class CollectionStats(_message.Message):
    __slots__ = ("total", "by_backend", "unbound")
    class ByBackendEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    BY_BACKEND_FIELD_NUMBER: _ClassVar[int]
    UNBOUND_FIELD_NUMBER: _ClassVar[int]
    total: int
    by_backend: _containers.ScalarMap[str, int]
    unbound: int
    def __init__(self, total: _Optional[int] = ..., by_backend: _Optional[_Mapping[str, int]] = ..., unbound: _Optional[int] = ...) -> None: ...

class UserStats(_message.Message):
    __slots__ = ("total", "disabled")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    total: int
    disabled: int
    def __init__(self, total: _Optional[int] = ..., disabled: _Optional[int] = ...) -> None: ...

class ObjectStateStat(_message.Message):
    __slots__ = ("state", "count", "bytes")
    STATE_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    BYTES_FIELD_NUMBER: _ClassVar[int]
    state: str
    count: int
    bytes: int
    def __init__(self, state: _Optional[str] = ..., count: _Optional[int] = ..., bytes: _Optional[int] = ...) -> None: ...

class TenantObjectStats(_message.Message):
    __slots__ = ("tenant_id", "slug", "display_name", "states", "total_count", "total_bytes")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SLUG_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    STATES_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    slug: str
    display_name: str
    states: _containers.RepeatedCompositeFieldContainer[ObjectStateStat]
    total_count: int
    total_bytes: int
    def __init__(self, tenant_id: _Optional[str] = ..., slug: _Optional[str] = ..., display_name: _Optional[str] = ..., states: _Optional[_Iterable[_Union[ObjectStateStat, _Mapping]]] = ..., total_count: _Optional[int] = ..., total_bytes: _Optional[int] = ...) -> None: ...

class ObjectStats(_message.Message):
    __slots__ = ("states", "total_count", "total_bytes", "tenants", "tenants_truncated", "tenants_next_page_token")
    STATES_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    TOTAL_BYTES_FIELD_NUMBER: _ClassVar[int]
    TENANTS_FIELD_NUMBER: _ClassVar[int]
    TENANTS_TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    TENANTS_NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    states: _containers.RepeatedCompositeFieldContainer[ObjectStateStat]
    total_count: int
    total_bytes: int
    tenants: _containers.RepeatedCompositeFieldContainer[TenantObjectStats]
    tenants_truncated: int
    tenants_next_page_token: str
    def __init__(self, states: _Optional[_Iterable[_Union[ObjectStateStat, _Mapping]]] = ..., total_count: _Optional[int] = ..., total_bytes: _Optional[int] = ..., tenants: _Optional[_Iterable[_Union[TenantObjectStats, _Mapping]]] = ..., tenants_truncated: _Optional[int] = ..., tenants_next_page_token: _Optional[str] = ...) -> None: ...
