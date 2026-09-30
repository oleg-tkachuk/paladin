import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from paladin.common.v1 import resource_pb2 as _resource_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RegenerateUploadUrlRequest(_message.Message):
    __slots__ = ("name", "ttl")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TTL_FIELD_NUMBER: _ClassVar[int]
    name: str
    ttl: _duration_pb2.Duration
    def __init__(self, name: _Optional[str] = ..., ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class RegenerateUploadUrlResponse(_message.Message):
    __slots__ = ("upload_url", "completion_mode")
    UPLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    COMPLETION_MODE_FIELD_NUMBER: _ClassVar[int]
    upload_url: _resource_pb2.PresignedUrl
    completion_mode: _resource_pb2.CompletionMode
    def __init__(self, upload_url: _Optional[_Union[_resource_pb2.PresignedUrl, _Mapping]] = ..., completion_mode: _Optional[_Union[_resource_pb2.CompletionMode, str]] = ...) -> None: ...

class PresignDownloadRequest(_message.Message):
    __slots__ = ("name", "ttl", "content_disposition")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TTL_FIELD_NUMBER: _ClassVar[int]
    CONTENT_DISPOSITION_FIELD_NUMBER: _ClassVar[int]
    name: str
    ttl: _duration_pb2.Duration
    content_disposition: str
    def __init__(self, name: _Optional[str] = ..., ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., content_disposition: _Optional[str] = ...) -> None: ...

class PresignDownloadResponse(_message.Message):
    __slots__ = ("download_url",)
    DOWNLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    download_url: _resource_pb2.PresignedUrl
    def __init__(self, download_url: _Optional[_Union[_resource_pb2.PresignedUrl, _Mapping]] = ...) -> None: ...
