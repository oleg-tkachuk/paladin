from buf.validate import validate_pb2 as _validate_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from paladin.iam.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class LoginRequest(_message.Message):
    __slots__ = ("subject", "password", "upstream_code", "requested_audience")
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    UPSTREAM_CODE_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    subject: str
    password: str
    upstream_code: str
    requested_audience: str
    def __init__(self, subject: _Optional[str] = ..., password: _Optional[str] = ..., upstream_code: _Optional[str] = ..., requested_audience: _Optional[str] = ...) -> None: ...

class LoginResponse(_message.Message):
    __slots__ = ("tokens", "user")
    TOKENS_FIELD_NUMBER: _ClassVar[int]
    USER_FIELD_NUMBER: _ClassVar[int]
    tokens: _types_pb2.TokenPair
    user: _types_pb2.User
    def __init__(self, tokens: _Optional[_Union[_types_pb2.TokenPair, _Mapping]] = ..., user: _Optional[_Union[_types_pb2.User, _Mapping]] = ...) -> None: ...

class RefreshTokenRequest(_message.Message):
    __slots__ = ("refresh_token", "requested_audience")
    REFRESH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    refresh_token: str
    requested_audience: str
    def __init__(self, refresh_token: _Optional[str] = ..., requested_audience: _Optional[str] = ...) -> None: ...

class RefreshTokenResponse(_message.Message):
    __slots__ = ("tokens",)
    TOKENS_FIELD_NUMBER: _ClassVar[int]
    tokens: _types_pb2.TokenPair
    def __init__(self, tokens: _Optional[_Union[_types_pb2.TokenPair, _Mapping]] = ...) -> None: ...

class RevokeRequest(_message.Message):
    __slots__ = ("token",)
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    token: str
    def __init__(self, token: _Optional[str] = ...) -> None: ...

class RevokeResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class WhoAmIRequest(_message.Message):
    __slots__ = ("route_page_token",)
    ROUTE_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    route_page_token: str
    def __init__(self, route_page_token: _Optional[str] = ...) -> None: ...

class WhoAmIResponse(_message.Message):
    __slots__ = ("user", "audience", "tenant_slug", "routes", "routes_truncated", "next_page_token")
    USER_FIELD_NUMBER: _ClassVar[int]
    AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    TENANT_SLUG_FIELD_NUMBER: _ClassVar[int]
    ROUTES_FIELD_NUMBER: _ClassVar[int]
    ROUTES_TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    NEXT_PAGE_TOKEN_FIELD_NUMBER: _ClassVar[int]
    user: _types_pb2.User
    audience: str
    tenant_slug: str
    routes: _containers.RepeatedCompositeFieldContainer[CollectionRoute]
    routes_truncated: bool
    next_page_token: str
    def __init__(self, user: _Optional[_Union[_types_pb2.User, _Mapping]] = ..., audience: _Optional[str] = ..., tenant_slug: _Optional[str] = ..., routes: _Optional[_Iterable[_Union[CollectionRoute, _Mapping]]] = ..., routes_truncated: _Optional[bool] = ..., next_page_token: _Optional[str] = ...) -> None: ...

class CollectionRoute(_message.Message):
    __slots__ = ("canonical", "tenant_path", "bare_alias", "backend", "bucket")
    CANONICAL_FIELD_NUMBER: _ClassVar[int]
    TENANT_PATH_FIELD_NUMBER: _ClassVar[int]
    BARE_ALIAS_FIELD_NUMBER: _ClassVar[int]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    BUCKET_FIELD_NUMBER: _ClassVar[int]
    canonical: str
    tenant_path: str
    bare_alias: str
    backend: str
    bucket: str
    def __init__(self, canonical: _Optional[str] = ..., tenant_path: _Optional[str] = ..., bare_alias: _Optional[str] = ..., backend: _Optional[str] = ..., bucket: _Optional[str] = ...) -> None: ...

class ChangePasswordRequest(_message.Message):
    __slots__ = ("old_password", "new_password")
    OLD_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    NEW_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    old_password: str
    new_password: str
    def __init__(self, old_password: _Optional[str] = ..., new_password: _Optional[str] = ...) -> None: ...

class ChangePasswordResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ExchangeAudienceRequest(_message.Message):
    __slots__ = ("refresh_token", "target_audience")
    REFRESH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    TARGET_AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    refresh_token: str
    target_audience: str
    def __init__(self, refresh_token: _Optional[str] = ..., target_audience: _Optional[str] = ...) -> None: ...

class ExchangeAudienceResponse(_message.Message):
    __slots__ = ("access_token", "access_expires_in_seconds", "token_type")
    ACCESS_TOKEN_FIELD_NUMBER: _ClassVar[int]
    ACCESS_EXPIRES_IN_SECONDS_FIELD_NUMBER: _ClassVar[int]
    TOKEN_TYPE_FIELD_NUMBER: _ClassVar[int]
    access_token: str
    access_expires_in_seconds: int
    token_type: str
    def __init__(self, access_token: _Optional[str] = ..., access_expires_in_seconds: _Optional[int] = ..., token_type: _Optional[str] = ...) -> None: ...

class ListMyMembershipsRequest(_message.Message):
    __slots__ = ("page",)
    PAGE_FIELD_NUMBER: _ClassVar[int]
    page: _pagination_pb2.PageRequest
    def __init__(self, page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class Membership(_message.Message):
    __slots__ = ("tenant_id", "tenant_slug", "roles", "disabled", "current")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_SLUG_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    DISABLED_FIELD_NUMBER: _ClassVar[int]
    CURRENT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    tenant_slug: str
    roles: _containers.RepeatedScalarFieldContainer[str]
    disabled: bool
    current: bool
    def __init__(self, tenant_id: _Optional[str] = ..., tenant_slug: _Optional[str] = ..., roles: _Optional[_Iterable[str]] = ..., disabled: _Optional[bool] = ..., current: _Optional[bool] = ...) -> None: ...

class ListMyMembershipsResponse(_message.Message):
    __slots__ = ("memberships", "page")
    MEMBERSHIPS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    memberships: _containers.RepeatedCompositeFieldContainer[Membership]
    page: _pagination_pb2.PageResponse
    def __init__(self, memberships: _Optional[_Iterable[_Union[Membership, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class SwitchTenantRequest(_message.Message):
    __slots__ = ("target_tenant_id", "requested_audience")
    TARGET_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_AUDIENCE_FIELD_NUMBER: _ClassVar[int]
    target_tenant_id: str
    requested_audience: str
    def __init__(self, target_tenant_id: _Optional[str] = ..., requested_audience: _Optional[str] = ...) -> None: ...

class SwitchTenantResponse(_message.Message):
    __slots__ = ("tokens", "user")
    TOKENS_FIELD_NUMBER: _ClassVar[int]
    USER_FIELD_NUMBER: _ClassVar[int]
    tokens: _types_pb2.TokenPair
    user: _types_pb2.User
    def __init__(self, tokens: _Optional[_Union[_types_pb2.TokenPair, _Mapping]] = ..., user: _Optional[_Union[_types_pb2.User, _Mapping]] = ...) -> None: ...
