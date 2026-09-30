from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class EnsureTenantStorageRequest(_message.Message):
    __slots__ = ("backend_id", "bucket", "collections")
    BACKEND_ID_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    COLLECTIONS_FIELD_NUMBER: _ClassVar[int]
    backend_id: str
    bucket: str
    collections: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, backend_id: _Optional[str] = ..., bucket: _Optional[str] = ..., collections: _Optional[_Iterable[str]] = ...) -> None: ...

class EnsureTenantStorageResponse(_message.Message):
    __slots__ = ("bucket_created", "collections_created", "collections_existing")
    BUCKET_CREATED_FIELD_NUMBER: _ClassVar[int]
    COLLECTIONS_CREATED_FIELD_NUMBER: _ClassVar[int]
    COLLECTIONS_EXISTING_FIELD_NUMBER: _ClassVar[int]
    bucket_created: bool
    collections_created: _containers.RepeatedScalarFieldContainer[str]
    collections_existing: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, bucket_created: _Optional[bool] = ..., collections_created: _Optional[_Iterable[str]] = ..., collections_existing: _Optional[_Iterable[str]] = ...) -> None: ...
