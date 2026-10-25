from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class FacetCount(_message.Message):
    __slots__ = ("facet", "value", "count")
    FACET_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    facet: str
    value: str
    count: int
    def __init__(self, facet: _Optional[str] = ..., value: _Optional[str] = ..., count: _Optional[int] = ...) -> None: ...
