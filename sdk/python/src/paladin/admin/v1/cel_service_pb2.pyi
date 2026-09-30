from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class ValidateCELRequest(_message.Message):
    __slots__ = ("schema", "expression")
    SCHEMA_FIELD_NUMBER: _ClassVar[int]
    EXPRESSION_FIELD_NUMBER: _ClassVar[int]
    schema: str
    expression: str
    def __init__(self, schema: _Optional[str] = ..., expression: _Optional[str] = ...) -> None: ...

class ValidateCELResponse(_message.Message):
    __slots__ = ("valid", "message", "line", "column")
    VALID_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    COLUMN_FIELD_NUMBER: _ClassVar[int]
    valid: bool
    message: str
    line: int
    column: int
    def __init__(self, valid: _Optional[bool] = ..., message: _Optional[str] = ..., line: _Optional[int] = ..., column: _Optional[int] = ...) -> None: ...
