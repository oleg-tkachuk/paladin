"""Delete an object, supplying the resource_version the server requires."""

from __future__ import annotations

from paladin.data.v1 import object_service_pb2
from paladin.errors import NotFoundError, VersionConflictError
from paladin.facade import AsyncDataPlane, DataPlane

DELETE_ATTEMPTS = 3
"""How many times ``delete`` reads an object again after it changed between
the read and the delete."""


def delete(data: DataPlane, name: str, *, permanent: bool = False) -> bool:
    """Remove the object called ``name``: read it, delete it at the version
    read, and when it changed in between read it again, up to
    ``DELETE_ATTEMPTS`` times. Returns whether this call removed it; an object
    already gone — NotFound on the read or the delete — is not an error, so an
    erasure that is retried, or that races another, succeeds.

    ``permanent`` destroys the object and its bytes instead of moving it to
    the trash; a public collection takes only a permanent delete."""
    conflict: VersionConflictError | None = None
    for _ in range(DELETE_ATTEMPTS):
        try:
            obj = data.object.get_object(object_service_pb2.GetObjectRequest(name=name))
            data.object.delete_object(
                object_service_pb2.DeleteObjectRequest(
                    name=name, resource_version=obj.resource_version, permanent=permanent
                )
            )
        except NotFoundError:
            return False
        except VersionConflictError as err:
            conflict = err
            continue
        return True
    assert conflict is not None  # the loop only falls through on a conflict
    raise conflict


async def adelete(data: AsyncDataPlane, name: str, *, permanent: bool = False) -> bool:
    """``delete`` for the async clients."""
    conflict: VersionConflictError | None = None
    for _ in range(DELETE_ATTEMPTS):
        try:
            obj = await data.object.get_object(object_service_pb2.GetObjectRequest(name=name))
            await data.object.delete_object(
                object_service_pb2.DeleteObjectRequest(
                    name=name, resource_version=obj.resource_version, permanent=permanent
                )
            )
        except NotFoundError:
            return False
        except VersionConflictError as err:
            conflict = err
            continue
        return True
    assert conflict is not None  # the loop only falls through on a conflict
    raise conflict
