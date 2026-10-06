"""Resource names and object URIs, built and parsed by the server's rules.

A tenant name takes the tenant's id or its slug; every name under a tenant
takes its id, a UUID. A collection may contain ``/``; object and version ids
are UUIDs. ``sdk/testdata/names.json`` holds the cases both SDKs are tested
against.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from urllib.parse import quote, unquote

_TENANTS = "tenants"
_COLLECTIONS = "collections"
_KEYS = "keys"
_TENANTS_PREFIX = _TENANTS + "/"
_COLLECTIONS_SEP = f"/{_COLLECTIONS}/"
_OBJECTS_SEP = "/objects/"
_VERSIONS_SEP = "/versions/"
_KEYS_SEP = f"/{_KEYS}/"
URI_SCHEME = "paladin"
"""The scheme of an object URI: ``paladin://tenants/…``."""
_URI_PREFIX = f"{URI_SCHEME}://{_TENANTS_PREFIX}"
# What a URI path segment leaves unescaped besides letters, digits and
# "_.-~": the characters Go's url.PathEscape leaves, so both SDKs print the
# same URI.
_SEGMENT_SAFE = "$&+=:@"
# {tenant}/collections/{collection}/keys/{key}
_URI_SEGMENTS = 5


class InvalidNameError(ValueError):
    """A resource name or object URI the server would refuse."""


def _invalid(kind: str, value: str, why: str) -> InvalidNameError:
    return InvalidNameError(f"{kind} {value!r}: {why}")


def _uuid(value: str) -> str | None:
    """The canonical, lower-case form of a UUID; None for anything else."""
    try:
        return str(uuid.UUID(value))
    except ValueError:
        return None


@dataclass(frozen=True)
class TenantName:
    """``tenants/{tenant}``: a tenant's id or its slug."""

    tenant: str

    def __post_init__(self) -> None:
        if not self.tenant or "/" in self.tenant:
            raise _invalid("tenant", self.tenant, "want a tenant id or slug, without '/'")

    def __str__(self) -> str:
        return _TENANTS_PREFIX + self.tenant

    @classmethod
    def parse(cls, value: str) -> TenantName:
        rest = value.removeprefix(_TENANTS_PREFIX)
        if rest == value or not rest or "/" in rest:
            raise _invalid("tenant", value, "want tenants/{id or slug}")
        return cls(rest)


@dataclass(frozen=True)
class CollectionName:
    """``tenants/{tenant-id}/collections/{collection}``; the collection may contain ``/``."""

    tenant: str
    collection: str

    def __post_init__(self) -> None:
        # Checked here as parse checks it, so a name built from parts is one
        # the server accepts: the tenant is its id, kept in canonical form.
        tenant_id = _uuid(self.tenant)
        if tenant_id is None:
            raise _invalid("collection", self.tenant, "the tenant must be its id, a UUID")
        if not self.collection:
            raise _invalid("collection", self.collection, "the collection is empty")
        object.__setattr__(self, "tenant", tenant_id)

    def __str__(self) -> str:
        return f"{_TENANTS_PREFIX}{self.tenant}{_COLLECTIONS_SEP}{self.collection}"

    @classmethod
    def parse(cls, value: str) -> CollectionName:
        want = "want tenants/{tenant-id}/collections/{collection}"
        rest = value.removeprefix(_TENANTS_PREFIX)
        tenant, found, collection = rest.partition(_COLLECTIONS_SEP)
        if rest == value or not found or not collection:
            raise _invalid("collection", value, want)
        tenant_id = _uuid(tenant)
        if tenant_id is None:
            raise _invalid("collection", value, "the tenant must be its id, a UUID")
        return cls(tenant_id, collection)


@dataclass(frozen=True)
class ObjectName:
    """``…/collections/{collection}/objects/{object-id}``."""

    collection: CollectionName
    object: str

    def __post_init__(self) -> None:
        object_id = _uuid(self.object)
        if object_id is None:
            raise _invalid("object", self.object, "the object must be its id, a UUID")
        object.__setattr__(self, "object", object_id)

    def __str__(self) -> str:
        return f"{self.collection}{_OBJECTS_SEP}{self.object}"

    @classmethod
    def parse(cls, value: str) -> ObjectName:
        head, found, tail = value.rpartition(_OBJECTS_SEP)
        if not found:
            raise _invalid("object", value, "want …/collections/{collection}/objects/{object-id}")
        collection = CollectionName.parse(head)
        object_id = _uuid(tail)
        if object_id is None:
            raise _invalid("object", value, "the object must be its id, a UUID")
        return cls(collection, object_id)


@dataclass(frozen=True)
class ObjectVersionName:
    """An object name followed by ``/versions/{version-id}``."""

    object: ObjectName
    version: str

    def __post_init__(self) -> None:
        version_id = _uuid(self.version)
        if version_id is None:
            raise _invalid("object version", self.version, "the version must be its id, a UUID")
        object.__setattr__(self, "version", version_id)

    def __str__(self) -> str:
        return f"{self.object}{_VERSIONS_SEP}{self.version}"

    @classmethod
    def parse(cls, value: str) -> ObjectVersionName:
        head, found, tail = value.rpartition(_VERSIONS_SEP)
        if not found:
            raise _invalid(
                "object version", value, "want …/objects/{object-id}/versions/{version-id}"
            )
        obj = ObjectName.parse(head)
        version_id = _uuid(tail)
        if version_id is None:
            raise _invalid("object version", value, "the version must be its id, a UUID")
        return cls(obj, version_id)


@dataclass(frozen=True)
class ObjectURI:
    """An object by its key, where a name addresses it by id:
    ``paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}``. The
    collection and the key are each one escaped path segment, so either may
    contain ``/``. It extends the resource-name space of the ``paladin://``
    resources the MCP bridge serves."""

    collection: CollectionName
    key: str

    def __post_init__(self) -> None:
        if not self.key:
            raise _invalid("object URI", self.key, "the key is empty")

    @property
    def parent(self) -> str:
        """The collection's name, the parent ``lookup_object`` takes."""
        return str(self.collection)

    def __str__(self) -> str:
        return (
            f"{_URI_PREFIX}{self.collection.tenant}{_COLLECTIONS_SEP}"
            f"{quote(self.collection.collection, safe=_SEGMENT_SAFE)}{_KEYS_SEP}"
            f"{quote(self.key, safe=_SEGMENT_SAFE)}"
        )

    @classmethod
    def parse(cls, value: str) -> ObjectURI:
        want = "want paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}"
        rest = value.removeprefix(_URI_PREFIX)
        segments = rest.split("/")
        if rest == value or len(segments) != _URI_SEGMENTS:
            raise _invalid("object URI", value, want)
        tenant, collections, collection, keys, key = segments
        if collections != _COLLECTIONS or keys != _KEYS:
            raise _invalid("object URI", value, want)
        tenant_id = _uuid(tenant)
        if tenant_id is None:
            raise _invalid("object URI", value, "the tenant must be its id, a UUID")
        collection, key = unquote(collection), unquote(key)
        if not collection or not key:
            raise _invalid("object URI", value, want)
        return cls(CollectionName(tenant_id, collection), key)


_STORAGE_BACKENDS_PREFIX = "storageBackends/"
_BUCKETS_SEP = "/buckets/"


@dataclass(frozen=True)
class BucketName:
    """``storageBackends/{backend}/buckets/{bucket}``: the name the admin plane
    gives a physical bucket, and a bucket quota's parent."""

    backend: str
    bucket: str

    def __post_init__(self) -> None:
        for part in (self.backend, self.bucket):
            if not part or "/" in part:
                raise _invalid("bucket", part, "want a backend and a bucket, each without '/'")

    def __str__(self) -> str:
        return f"{_STORAGE_BACKENDS_PREFIX}{self.backend}{_BUCKETS_SEP}{self.bucket}"

    @classmethod
    def parse(cls, value: str) -> BucketName:
        want = "want storageBackends/{backend}/buckets/{bucket}"
        rest = value.removeprefix(_STORAGE_BACKENDS_PREFIX)
        backend, found, bucket = rest.partition(_BUCKETS_SEP)
        if rest == value or not found:
            raise _invalid("bucket", value, want)
        try:
            return cls(backend, bucket)
        except InvalidNameError:
            raise _invalid("bucket", value, want) from None


API_TOKEN_PREFIX = "paladin_pat_"
"""Starts every Paladin API token: the server tells an API token from a JWT by
it, and secret scanners find one by it."""

OBJECT_RESOURCE_SCHEME = "object://"
"""The scheme of the resource a capability grants on objects."""


def object_resource(tenant: str, collection: str, key: str) -> str:
    """The resource a capability names for an object, or for every object under
    a key prefix when ``key`` is one: the server checks a capability's resource
    against ``object://{tenant-id}/{collection}/{key}``. The tenant is the id,
    a UUID; give ``"reports/"`` to cover the keys under ``reports/``."""
    return f"{OBJECT_RESOURCE_SCHEME}{tenant}/{collection}/{key}"
