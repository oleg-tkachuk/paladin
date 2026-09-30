from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CompletionMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    COMPLETION_MODE_UNSPECIFIED: _ClassVar[CompletionMode]
    COMPLETION_MODE_IMPLICIT: _ClassVar[CompletionMode]
    COMPLETION_MODE_EXPLICIT: _ClassVar[CompletionMode]

class ChecksumAlgorithm(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CHECKSUM_ALGORITHM_UNSPECIFIED: _ClassVar[ChecksumAlgorithm]
    CHECKSUM_ALGORITHM_CRC32C: _ClassVar[ChecksumAlgorithm]
    CHECKSUM_ALGORITHM_SHA256: _ClassVar[ChecksumAlgorithm]
    CHECKSUM_ALGORITHM_MD5: _ClassVar[ChecksumAlgorithm]
COMPLETION_MODE_UNSPECIFIED: CompletionMode
COMPLETION_MODE_IMPLICIT: CompletionMode
COMPLETION_MODE_EXPLICIT: CompletionMode
CHECKSUM_ALGORITHM_UNSPECIFIED: ChecksumAlgorithm
CHECKSUM_ALGORITHM_CRC32C: ChecksumAlgorithm
CHECKSUM_ALGORITHM_SHA256: ChecksumAlgorithm
CHECKSUM_ALGORITHM_MD5: ChecksumAlgorithm

class PresignedUrl(_message.Message):
    __slots__ = ("url", "method", "required_headers", "post_policy", "expires_at_rfc3339")
    class RequiredHeadersEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    URL_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    REQUIRED_HEADERS_FIELD_NUMBER: _ClassVar[int]
    POST_POLICY_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_AT_RFC3339_FIELD_NUMBER: _ClassVar[int]
    url: str
    method: str
    required_headers: _containers.ScalarMap[str, str]
    post_policy: PresignedPostPolicy
    expires_at_rfc3339: str
    def __init__(self, url: _Optional[str] = ..., method: _Optional[str] = ..., required_headers: _Optional[_Mapping[str, str]] = ..., post_policy: _Optional[_Union[PresignedPostPolicy, _Mapping]] = ..., expires_at_rfc3339: _Optional[str] = ...) -> None: ...

class PresignedPostPolicy(_message.Message):
    __slots__ = ("fields", "action")
    class FieldsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    fields: _containers.ScalarMap[str, str]
    action: str
    def __init__(self, fields: _Optional[_Mapping[str, str]] = ..., action: _Optional[str] = ...) -> None: ...
