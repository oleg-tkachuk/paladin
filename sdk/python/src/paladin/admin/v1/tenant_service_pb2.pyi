import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PurgeBlockerKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PURGE_BLOCKER_KIND_UNSPECIFIED: _ClassVar[PurgeBlockerKind]
    PURGE_BLOCKER_KIND_COLLECTIONS: _ClassVar[PurgeBlockerKind]
    PURGE_BLOCKER_KIND_BUCKETS: _ClassVar[PurgeBlockerKind]
    PURGE_BLOCKER_KIND_USERS: _ClassVar[PurgeBlockerKind]
PURGE_BLOCKER_KIND_UNSPECIFIED: PurgeBlockerKind
PURGE_BLOCKER_KIND_COLLECTIONS: PurgeBlockerKind
PURGE_BLOCKER_KIND_BUCKETS: PurgeBlockerKind
PURGE_BLOCKER_KIND_USERS: PurgeBlockerKind

class CreateTenantRequest(_message.Message):
    __slots__ = ("tenant_id", "tenant", "default_bucket")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_BUCKET_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    tenant: _types_pb2.Tenant
    default_bucket: str
    def __init__(self, tenant_id: _Optional[str] = ..., tenant: _Optional[_Union[_types_pb2.Tenant, _Mapping]] = ..., default_bucket: _Optional[str] = ...) -> None: ...

class GetTenantRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateTenantRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "tenant")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    TENANT_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    tenant: _types_pb2.Tenant
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., tenant: _Optional[_Union[_types_pb2.Tenant, _Mapping]] = ...) -> None: ...

class DeleteTenantRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class DeleteTenantResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListTenantsRequest(_message.Message):
    __slots__ = ("page", "filter", "include_trashed", "only_trashed")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_TRASHED_FIELD_NUMBER: _ClassVar[int]
    ONLY_TRASHED_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    filter: str
    include_trashed: bool
    only_trashed: bool
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., include_trashed: _Optional[bool] = ..., only_trashed: _Optional[bool] = ...) -> None: ...

class ListTenantsResponse(_message.Message):
    __slots__ = ("tenants", "page")
    TENANTS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenants: _containers.RepeatedCompositeFieldContainer[_types_pb2.Tenant]
    page: _pagination_pb2.PageResponse
    def __init__(self, tenants: _Optional[_Iterable[_Union[_types_pb2.Tenant, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class SetInheritedPolicyRequest(_message.Message):
    __slots__ = ("name", "resource_version", "cedar_policy")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    cedar_policy: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., cedar_policy: _Optional[str] = ...) -> None: ...

class RestoreTenantRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class PurgeTenantRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class PurgeTenantResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class PreviewPurgeTenantRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class PreviewPurgeTenantResponse(_message.Message):
    __slots__ = ("api_tokens", "api_token_count", "event_subscriptions", "event_subscription_count", "capability_count", "blockers")
    API_TOKENS_FIELD_NUMBER: _ClassVar[int]
    API_TOKEN_COUNT_FIELD_NUMBER: _ClassVar[int]
    EVENT_SUBSCRIPTIONS_FIELD_NUMBER: _ClassVar[int]
    EVENT_SUBSCRIPTION_COUNT_FIELD_NUMBER: _ClassVar[int]
    CAPABILITY_COUNT_FIELD_NUMBER: _ClassVar[int]
    BLOCKERS_FIELD_NUMBER: _ClassVar[int]
    api_tokens: _containers.RepeatedCompositeFieldContainer[PurgedResource]
    api_token_count: int
    event_subscriptions: _containers.RepeatedCompositeFieldContainer[PurgedResource]
    event_subscription_count: int
    capability_count: int
    blockers: _containers.RepeatedCompositeFieldContainer[PurgeBlocker]
    def __init__(self, api_tokens: _Optional[_Iterable[_Union[PurgedResource, _Mapping]]] = ..., api_token_count: _Optional[int] = ..., event_subscriptions: _Optional[_Iterable[_Union[PurgedResource, _Mapping]]] = ..., event_subscription_count: _Optional[int] = ..., capability_count: _Optional[int] = ..., blockers: _Optional[_Iterable[_Union[PurgeBlocker, _Mapping]]] = ...) -> None: ...

class PurgedResource(_message.Message):
    __slots__ = ("name", "display_name")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    display_name: str
    def __init__(self, name: _Optional[str] = ..., display_name: _Optional[str] = ...) -> None: ...

class PurgeBlocker(_message.Message):
    __slots__ = ("kind", "count")
    KIND_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    kind: PurgeBlockerKind
    count: int
    def __init__(self, kind: _Optional[_Union[PurgeBlockerKind, str]] = ..., count: _Optional[int] = ...) -> None: ...

class RenameTenantSlugRequest(_message.Message):
    __slots__ = ("name", "resource_version", "new_slug")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    NEW_SLUG_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    new_slug: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., new_slug: _Optional[str] = ...) -> None: ...

class ResolveRenamedSlugRequest(_message.Message):
    __slots__ = ("old_slug",)
    OLD_SLUG_FIELD_NUMBER: _ClassVar[int]
    old_slug: str
    def __init__(self, old_slug: _Optional[str] = ...) -> None: ...

class ResolveRenamedSlugResponse(_message.Message):
    __slots__ = ("new_slug", "renamed_at")
    NEW_SLUG_FIELD_NUMBER: _ClassVar[int]
    RENAMED_AT_FIELD_NUMBER: _ClassVar[int]
    new_slug: str
    renamed_at: _timestamp_pb2.Timestamp
    def __init__(self, new_slug: _Optional[str] = ..., renamed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MigrateTenantStorageLayoutRequest(_message.Message):
    __slots__ = ("name", "target_backend_id", "cleanup_retention_seconds")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TARGET_BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    CLEANUP_RETENTION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    name: str
    target_backend_id: str
    cleanup_retention_seconds: int
    def __init__(self, name: _Optional[str] = ..., target_backend_id: _Optional[str] = ..., cleanup_retention_seconds: _Optional[int] = ...) -> None: ...

class GetTenantStorageMigrationRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class StorageMigrationStatus(_message.Message):
    __slots__ = ("tenant", "state", "objects_total", "objects_copied", "source_bucket", "target_bucket", "error")
    TENANT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    OBJECTS_TOTAL_FIELD_NUMBER: _ClassVar[int]
    OBJECTS_COPIED_FIELD_NUMBER: _ClassVar[int]
    SOURCE_BUCKET_FIELD_NUMBER: _ClassVar[int]
    TARGET_BUCKET_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    tenant: str
    state: str
    objects_total: int
    objects_copied: int
    source_bucket: str
    target_bucket: str
    error: str
    def __init__(self, tenant: _Optional[str] = ..., state: _Optional[str] = ..., objects_total: _Optional[int] = ..., objects_copied: _Optional[int] = ..., source_bucket: _Optional[str] = ..., target_bucket: _Optional[str] = ..., error: _Optional[str] = ...) -> None: ...

class TenantDefaultBinding(_message.Message):
    __slots__ = ("name", "bucket", "set_at", "set_by")
    NAME_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    SET_AT_FIELD_NUMBER: _ClassVar[int]
    SET_BY_FIELD_NUMBER: _ClassVar[int]
    name: str
    bucket: str
    set_at: _timestamp_pb2.Timestamp
    set_by: str
    def __init__(self, name: _Optional[str] = ..., bucket: _Optional[str] = ..., set_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., set_by: _Optional[str] = ...) -> None: ...

class GetTenantDefaultBindingRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class SetTenantDefaultBindingRequest(_message.Message):
    __slots__ = ("name", "bucket")
    NAME_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    name: str
    bucket: str
    def __init__(self, name: _Optional[str] = ..., bucket: _Optional[str] = ...) -> None: ...

class ClearTenantDefaultBindingRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class ClearTenantDefaultBindingResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
