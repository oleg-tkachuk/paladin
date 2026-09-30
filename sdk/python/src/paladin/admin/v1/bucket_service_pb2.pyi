from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from paladin.admin.v1 import types_pb2 as _types_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateBucketRequest(_message.Message):
    __slots__ = ("parent", "bucket_id", "bucket", "provision_on_backend")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    BUCKET_ID_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    PROVISION_ON_BACKEND_FIELD_NUMBER: _ClassVar[int]
    parent: str
    bucket_id: str
    bucket: _types_pb2.Bucket
    provision_on_backend: bool
    def __init__(self, parent: _Optional[str] = ..., bucket_id: _Optional[str] = ..., bucket: _Optional[_Union[_types_pb2.Bucket, _Mapping]] = ..., provision_on_backend: _Optional[bool] = ...) -> None: ...

class GetBucketRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateBucketRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "bucket")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    bucket: _types_pb2.Bucket
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., bucket: _Optional[_Union[_types_pb2.Bucket, _Mapping]] = ...) -> None: ...

class DeleteBucketRequest(_message.Message):
    __slots__ = ("name", "resource_version", "delete_on_backend", "skip_version_check")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    DELETE_ON_BACKEND_FIELD_NUMBER: _ClassVar[int]
    SKIP_VERSION_CHECK_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    delete_on_backend: bool
    skip_version_check: bool
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., delete_on_backend: _Optional[bool] = ..., skip_version_check: _Optional[bool] = ...) -> None: ...

class DeleteBucketResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListBucketsRequest(_message.Message):
    __slots__ = ("parent", "page", "filter", "owner_tenant_id")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    OWNER_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    filter: str
    owner_tenant_id: str
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., owner_tenant_id: _Optional[str] = ...) -> None: ...

class ListBucketsResponse(_message.Message):
    __slots__ = ("buckets", "page")
    BUCKETS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    buckets: _containers.RepeatedCompositeFieldContainer[_types_pb2.Bucket]
    page: _pagination_pb2.PageResponse
    def __init__(self, buckets: _Optional[_Iterable[_Union[_types_pb2.Bucket, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class SetBucketPolicyRequest(_message.Message):
    __slots__ = ("name", "resource_version", "cedar_policy")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    cedar_policy: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., cedar_policy: _Optional[str] = ...) -> None: ...

class SetLifecycleRulesRequest(_message.Message):
    __slots__ = ("name", "resource_version", "rules")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    RULES_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    rules: _containers.RepeatedCompositeFieldContainer[_types_pb2.LifecycleRule]
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., rules: _Optional[_Iterable[_Union[_types_pb2.LifecycleRule, _Mapping]]] = ...) -> None: ...

class SetObjectLockRequest(_message.Message):
    __slots__ = ("name", "resource_version", "config")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    config: _types_pb2.ObjectLockConfig
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., config: _Optional[_Union[_types_pb2.ObjectLockConfig, _Mapping]] = ...) -> None: ...

class SetVersioningRequest(_message.Message):
    __slots__ = ("name", "resource_version", "versioning")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    VERSIONING_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    versioning: _types_pb2.BucketVersioning
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., versioning: _Optional[_Union[_types_pb2.BucketVersioning, _Mapping]] = ...) -> None: ...

class SetReplicationRequest(_message.Message):
    __slots__ = ("name", "resource_version", "replication")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    REPLICATION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    replication: _types_pb2.BucketReplication
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., replication: _Optional[_Union[_types_pb2.BucketReplication, _Mapping]] = ...) -> None: ...

class ListAccessibleBucketsRequest(_message.Message):
    __slots__ = ("tenant", "page")
    TENANT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant: str
    page: _pagination_pb2.PageRequest
    def __init__(self, tenant: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...
