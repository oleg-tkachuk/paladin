"""Find a NUL character (U+0000) in the strings of a protobuf message, as the
server does before it serves a request.

The server refuses such a request as INVALID_ARGUMENT, naming the field:
``key: holds a NUL character``. ``paladin.testing`` refuses it the same way
with this module. The server's walker and the Go SDK's are held to the same
paths by ``sdk/testdata/request_nul.json``.

Every string is checked: singular and repeated fields, map keys and map
values, the fields of nested messages and of a set oneof, and well-known types
such as StringValue and Struct, which are messages like any other. Bytes
fields are not text and may carry anything. Fields are walked in declaration
order, as the server walks them, then extensions.
"""

from __future__ import annotations

from typing import Any

from google.protobuf.descriptor import FieldDescriptor
from google.protobuf.message import Message

NUL = "\x00"
"""The character no request string may hold."""
REFUSAL_SUFFIX = ": holds a NUL character"
"""What follows the field's path in the refusal. The value itself is never
part of it."""

_MAP_KEY = "key"
_MAP_VALUE = "value"
"""The path elements a map entry's two halves are named by: the names protobuf
gives a map entry's fields."""
_SEPARATOR = "."
_INDEX_OPEN = "["
_INDEX_CLOSE = "]"
_EXTENSION_OPEN = "("
_EXTENSION_CLOSE = ")"
"""The punctuation of a path: a dot between nested fields, brackets around a
list index, parentheses around an extension's full name."""


def find(message: Message) -> str | None:
    """The path of the first string in ``message`` that holds NUL, or None
    when none does."""
    return _find_message(message)


def refusal(path: str) -> str:
    """The refusal's text for a NUL at ``path``."""
    return path + REFUSAL_SUFFIX


def _find_message(message: Message) -> str | None:
    present = message.ListFields()
    by_number = {fd.number: value for fd, value in present if not fd.is_extension}
    for fd in message.DESCRIPTOR.fields:
        if fd.number not in by_number:
            continue
        rest = _find_field(fd, by_number[fd.number])
        if rest is not None:
            return _join(fd.name, rest)
    for fd, value in present:
        if not fd.is_extension:
            continue
        rest = _find_field(fd, value)
        if rest is not None:
            return _join(_EXTENSION_OPEN + fd.full_name + _EXTENSION_CLOSE, rest)
    return None


# Descriptors and field values below are typed Any: the upb runtime hands out
# its own FieldDescriptor class beside the pure-Python one, and a field's value
# is a string, a message, or a repeated or map container depending on fd.
def _find_field(fd: Any, value: Any) -> str | None:
    if _is_map(fd):
        return _find_map(fd, value)
    if fd.is_repeated:
        for i, item in enumerate(value):
            rest = _find_singular(fd, item)
            if rest is not None:
                return _join(f"{_INDEX_OPEN}{i}{_INDEX_CLOSE}", rest)
        return None
    return _find_singular(fd, value)


def _find_map(fd: Any, value: Any) -> str | None:
    """Whether a key or a value of the map field ``fd`` holds NUL. It names
    which, never the key itself, which may be the string holding NUL."""
    entry = fd.message_type.fields_by_name
    key_is_string = entry[_MAP_KEY].type == FieldDescriptor.TYPE_STRING
    value_fd = entry[_MAP_VALUE]
    for k, v in value.items():
        if key_is_string and NUL in k:
            return _MAP_KEY
        rest = _find_singular(value_fd, v)
        if rest is not None:
            return _join(_MAP_VALUE, rest)
    return None


def _find_singular(fd: Any, value: Any) -> str | None:
    """``""`` when one value of ``fd``'s kind holds NUL itself, the rest of
    the path when a message's field does, None when nothing does. Every other
    kind, bytes included, holds no text."""
    if fd.type == FieldDescriptor.TYPE_STRING:
        return "" if NUL in value else None
    if fd.type in (FieldDescriptor.TYPE_MESSAGE, FieldDescriptor.TYPE_GROUP):
        return _find_message(value)
    return None


def _join(head: str, rest: str) -> str:
    """``head`` in front of the rest of a path: a dot between them, unless the
    rest is empty or starts with a list index, which follows its field
    directly."""
    if not rest or rest.startswith(_INDEX_OPEN):
        return head + rest
    return head + _SEPARATOR + rest


def _is_map(fd: Any) -> bool:
    return bool(
        fd.type == FieldDescriptor.TYPE_MESSAGE
        and fd.is_repeated
        and fd.message_type.GetOptions().map_entry
    )
