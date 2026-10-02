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

class CreateSubscriptionRequest(_message.Message):
    __slots__ = ("parent", "subscription")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIPTION_FIELD_NUMBER: _ClassVar[int]
    parent: str
    subscription: _types_pb2.EventSubscription
    def __init__(self, parent: _Optional[str] = ..., subscription: _Optional[_Union[_types_pb2.EventSubscription, _Mapping]] = ...) -> None: ...

class GetSubscriptionRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class UpdateSubscriptionRequest(_message.Message):
    __slots__ = ("name", "resource_version", "update_mask", "subscription")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    update_mask: _field_mask_pb2.FieldMask
    subscription: _types_pb2.EventSubscription
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ..., subscription: _Optional[_Union[_types_pb2.EventSubscription, _Mapping]] = ...) -> None: ...

class DeleteSubscriptionRequest(_message.Message):
    __slots__ = ("name", "resource_version")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    resource_version: str
    def __init__(self, name: _Optional[str] = ..., resource_version: _Optional[str] = ...) -> None: ...

class DeleteSubscriptionResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListSubscriptionsRequest(_message.Message):
    __slots__ = ("parent", "page")
    PARENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    parent: str
    page: _pagination_pb2.PageRequest
    def __init__(self, parent: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListSubscriptionsResponse(_message.Message):
    __slots__ = ("subscriptions", "page")
    SUBSCRIPTIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    subscriptions: _containers.RepeatedCompositeFieldContainer[_types_pb2.EventSubscription]
    page: _pagination_pb2.PageResponse
    def __init__(self, subscriptions: _Optional[_Iterable[_Union[_types_pb2.EventSubscription, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class TestSubscriptionRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class TestSubscriptionResponse(_message.Message):
    __slots__ = ("delivered", "status_code", "error_message")
    DELIVERED_FIELD_NUMBER: _ClassVar[int]
    STATUS_CODE_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    delivered: bool
    status_code: int
    error_message: str
    def __init__(self, delivered: _Optional[bool] = ..., status_code: _Optional[int] = ..., error_message: _Optional[str] = ...) -> None: ...

class RedriveFailedDeliveriesRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class RedriveFailedDeliveriesResponse(_message.Message):
    __slots__ = ("requeued",)
    REQUEUED_FIELD_NUMBER: _ClassVar[int]
    requeued: int
    def __init__(self, requeued: _Optional[int] = ...) -> None: ...
