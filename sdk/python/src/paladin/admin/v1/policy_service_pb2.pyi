import datetime

from buf.validate import validate_pb2 as _validate_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from paladin.common.v1 import pagination_pb2 as _pagination_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ValidateRequest(_message.Message):
    __slots__ = ("cedar_policy",)
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    cedar_policy: str
    def __init__(self, cedar_policy: _Optional[str] = ...) -> None: ...

class ValidateResponse(_message.Message):
    __slots__ = ("ok", "diagnostics")
    OK_FIELD_NUMBER: _ClassVar[int]
    DIAGNOSTICS_FIELD_NUMBER: _ClassVar[int]
    ok: bool
    diagnostics: _containers.RepeatedCompositeFieldContainer[PolicyDiagnostic]
    def __init__(self, ok: _Optional[bool] = ..., diagnostics: _Optional[_Iterable[_Union[PolicyDiagnostic, _Mapping]]] = ...) -> None: ...

class PolicyDiagnostic(_message.Message):
    __slots__ = ("severity", "message", "line", "column")
    SEVERITY_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    COLUMN_FIELD_NUMBER: _ClassVar[int]
    severity: str
    message: str
    line: int
    column: int
    def __init__(self, severity: _Optional[str] = ..., message: _Optional[str] = ..., line: _Optional[int] = ..., column: _Optional[int] = ...) -> None: ...

class SimulateAuthzRequest(_message.Message):
    __slots__ = ("principal_subject", "principal_tenant_id", "principal_roles", "action", "resource_name", "principal_kind")
    PRINCIPAL_SUBJECT_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_ROLES_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_NAME_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_KIND_FIELD_NUMBER: _ClassVar[int]
    principal_subject: str
    principal_tenant_id: str
    principal_roles: _containers.RepeatedScalarFieldContainer[str]
    action: str
    resource_name: str
    principal_kind: str
    def __init__(self, principal_subject: _Optional[str] = ..., principal_tenant_id: _Optional[str] = ..., principal_roles: _Optional[_Iterable[str]] = ..., action: _Optional[str] = ..., resource_name: _Optional[str] = ..., principal_kind: _Optional[str] = ...) -> None: ...

class SimulateAuthzResponse(_message.Message):
    __slots__ = ("allowed", "matched_policies", "explanation")
    ALLOWED_FIELD_NUMBER: _ClassVar[int]
    MATCHED_POLICIES_FIELD_NUMBER: _ClassVar[int]
    EXPLANATION_FIELD_NUMBER: _ClassVar[int]
    allowed: bool
    matched_policies: _containers.RepeatedScalarFieldContainer[str]
    explanation: str
    def __init__(self, allowed: _Optional[bool] = ..., matched_policies: _Optional[_Iterable[str]] = ..., explanation: _Optional[str] = ...) -> None: ...

class GetEffectivePolicyRequest(_message.Message):
    __slots__ = ("resource_name",)
    RESOURCE_NAME_FIELD_NUMBER: _ClassVar[int]
    resource_name: str
    def __init__(self, resource_name: _Optional[str] = ...) -> None: ...

class GetEffectivePolicyResponse(_message.Message):
    __slots__ = ("merged_cedar_policy", "layers")
    MERGED_CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    LAYERS_FIELD_NUMBER: _ClassVar[int]
    merged_cedar_policy: str
    layers: _containers.RepeatedCompositeFieldContainer[PolicyLayer]
    def __init__(self, merged_cedar_policy: _Optional[str] = ..., layers: _Optional[_Iterable[_Union[PolicyLayer, _Mapping]]] = ...) -> None: ...

class PolicyLayer(_message.Message):
    __slots__ = ("source", "cedar_policy", "frozen", "evaluated_cedar_policy")
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    FROZEN_FIELD_NUMBER: _ClassVar[int]
    EVALUATED_CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    source: str
    cedar_policy: str
    frozen: bool
    evaluated_cedar_policy: str
    def __init__(self, source: _Optional[str] = ..., cedar_policy: _Optional[str] = ..., frozen: _Optional[bool] = ..., evaluated_cedar_policy: _Optional[str] = ...) -> None: ...

class ListPolicyVersionsRequest(_message.Message):
    __slots__ = ("resource_name", "page")
    RESOURCE_NAME_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    resource_name: str
    page: _pagination_pb2.PageRequest
    def __init__(self, resource_name: _Optional[str] = ..., page: _Optional[_Union[_pagination_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListPolicyVersionsResponse(_message.Message):
    __slots__ = ("versions", "page")
    VERSIONS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    versions: _containers.RepeatedCompositeFieldContainer[PolicyVersion]
    page: _pagination_pb2.PageResponse
    def __init__(self, versions: _Optional[_Iterable[_Union[PolicyVersion, _Mapping]]] = ..., page: _Optional[_Union[_pagination_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class PolicyVersion(_message.Message):
    __slots__ = ("version", "cedar_policy", "written_by", "written_at", "written_by_tenant_id")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    CEDAR_POLICY_FIELD_NUMBER: _ClassVar[int]
    WRITTEN_BY_FIELD_NUMBER: _ClassVar[int]
    WRITTEN_AT_FIELD_NUMBER: _ClassVar[int]
    WRITTEN_BY_TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    version: int
    cedar_policy: str
    written_by: str
    written_at: _timestamp_pb2.Timestamp
    written_by_tenant_id: str
    def __init__(self, version: _Optional[int] = ..., cedar_policy: _Optional[str] = ..., written_by: _Optional[str] = ..., written_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., written_by_tenant_id: _Optional[str] = ...) -> None: ...
