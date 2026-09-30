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

class CreateCollectionRequest(_message.Message):
    __slots__ = ("parent", "collection", "collection_resource")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    COLLECTION_FIELD_NUMBER: _ClassVar[int]
    COLLECTION_RESOURCE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    collection: str
    collection_resource: _types_pb2.Collection
    def __init__(self, parent: _Optional[str] = ..., collection: _Optional[str] = ..., collection_resource: _Optional[_Union[_types_pb2.Collection, _Mapping]] = ...) -> None: ...

class GetCollectionRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateCollectionRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "collection_resource")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    COLLECTION_RESOURCE_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    collection_resource: _types_pb2.Collection
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., collection_resource: _Optional[_Union[_types_pb2.Collection, _Mapping]] = ...) -> None: ...

class DeleteCollectionRequest(_message.Message):
    __slots__ = ("name", "resource_version", "skip_version_check")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    SKIP_VERSION_CHECK_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    skip_version_check: bool
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., skip_version_check: _Optional[bool] = ...) -> None: ...

class DeleteCollectionResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListCollectionsRequest(_message.Message):
    __slots__ = ("parent", "page", "filter", "bucket")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    filter: str
    bucket: str
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ..., filter: _Optional[str] = ..., bucket: _Optional[str] = ...) -> None: ...

class ListCollectionsResponse(_message.Message):
    __slots__ = ("collections", "page")
    COLLECTIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    collections: _containers.RepeatedCompositeFieldContainer[_types_pb2.Collection]
    page: _pagination_pb2.PageResponse
    def __init__(self, collections: _Optional[_Iterable[_Union[_types_pb2.Collection, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class SetCollectionPolicyRequest(_message.Message):
    __slots__ = ("name", "resource_version", "cedar_policy")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    cedar_policy: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., cedar_policy: _Optional[str] = ...) -> None: ...

class BindCollectionToBucketRequest(_message.Message):
    __slots__ = ("name", "resource_version", "bucket")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    bucket: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., bucket: _Optional[str] = ...) -> None: ...
