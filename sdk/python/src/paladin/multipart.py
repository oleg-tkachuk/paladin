"""The control half of a multipart upload whose bytes another party sends.

A browser or an edge worker sends the parts while this process holds the
credentials: ``begin_multipart`` opens the upload, ``presign_part`` signs each
part once its sender has hashed it, ``complete_multipart`` assembles them,
``abort_multipart`` drops them. ``upload`` does all four itself when it holds
the bytes. Each has an ``a…`` form for the async clients.
"""

from __future__ import annotations

from collections.abc import Iterable

from connectrpc.code import Code
from connectrpc.errors import ConnectError

from paladin.client import _own_keys
from paladin.common.v1 import resource_pb2
from paladin.data.v1 import multipart_service_pb2, object_service_pb2, types_pb2
from paladin.facade import AsyncDataPlane, DataPlane
from paladin.transfer import TransferError
from paladin.workflows import UploadSession, _initiate_request

_PUT = "PUT"
_NO_URL = "the server returned no upload URL"

_ETAG_QUOTE = '"'


class NoPartSplitError(Exception):
    """The server named no part size or part count for a multipart upload.

    Every part URL is signed for the size the server recommended, so a sender
    cannot pick a split of its own; ``begin_multipart`` aborts the upload and
    raises this."""


def _session(resp: multipart_service_pb2.InitiateMultipartUploadResponse) -> UploadSession:
    return UploadSession(
        object_name=resp.object.name,
        upload_id=resp.upload_id,
        part_size=resp.recommended_part_size,
        total_parts=resp.total_parts,
    )


def _split(session: UploadSession) -> bool:
    return session.part_size > 0 and session.total_parts > 0


def _presign_request(
    session: UploadSession, number: int, checksum: str
) -> multipart_service_pb2.PresignPartRequest:
    return multipart_service_pb2.PresignPartRequest(
        object_name=session.object_name,
        upload_id=session.upload_id,
        part_number=number,
        checksum_value=checksum,
    )


def _complete_request(
    session: UploadSession, parts: Iterable[types_pb2.CompletedPart]
) -> multipart_service_pb2.CompleteMultipartUploadRequest:
    return multipart_service_pb2.CompleteMultipartUploadRequest(
        object_name=session.object_name,
        upload_id=session.upload_id,
        parts=[
            types_pb2.CompletedPart(
                part_number=p.part_number,
                etag=p.etag.strip().strip(_ETAG_QUOTE),
                checksum_value=p.checksum_value,
            )
            for p in parts
        ],
    )


def _abort_request(session: UploadSession) -> multipart_service_pb2.AbortMultipartUploadRequest:
    return multipart_service_pb2.AbortMultipartUploadRequest(
        object_name=session.object_name, upload_id=session.upload_id
    )


def _gone(err: ConnectError) -> bool:
    return err.code == Code.NOT_FOUND


def begin_multipart(
    data: DataPlane,
    *,
    parent: str,
    content_type: str,
    size: int,
    key: str = "",
    metadata: dict[str, str] | None = None,
    tags: dict[str, str] | None = None,
) -> UploadSession:
    """Reserve an object for ``size`` bytes and open a multipart upload on it,
    split as the server recommends: the session's ``part_size`` and
    ``total_parts`` are what the sender must slice the bytes into. Nothing is
    presigned yet — a part URL is signed for the part's checksum, which only
    the holder of the bytes can compute."""
    session = _session(
        data.multipart_upload.initiate_multipart_upload(
            _initiate_request(parent, key, content_type, size, metadata, tags)
        )
    )
    if not _split(session):
        abort_multipart(data, session)
        raise NoPartSplitError(f"no part size for {session.object_name}")
    return session


def presign_part(
    data: DataPlane, session: UploadSession, number: int, checksum: str
) -> resource_pb2.PresignedUrl:
    """Sign part ``number`` (1-based) of an open upload for its checksum, the
    base64 SHA-256 of its bytes: storage refuses a body of any other size or
    digest. Call it again for a part whose URL expired. Every call mints a
    fresh URL, so none shares the block's idempotency key."""
    with _own_keys():
        signed = data.multipart_upload.presign_part(
            _presign_request(session, number, checksum)
        ).upload_url
    if not signed.url:
        raise TransferError(_PUT, "", 0, _NO_URL)
    return signed


def complete_multipart(
    data: DataPlane, session: UploadSession, parts: Iterable[types_pb2.CompletedPart]
) -> types_pb2.Object:
    """Assemble the parts — each with the ETag storage answered its PUT with,
    and the checksum it was presigned for — into the object, and return the
    object as stored. An ETag is taken as a browser reads it from the response
    header, quotes and all. A server that answers with the object's name
    alone, as older releases do, is read back with ``get_object`` when the
    caller may read it, and its answer returned as it is otherwise."""
    done = data.multipart_upload.complete_multipart_upload(_complete_request(session, parts))
    if done.collection:
        return done
    try:
        return data.object.get_object(object_service_pb2.GetObjectRequest(name=session.object_name))
    except ConnectError:
        # The upload is complete; a caller allowed to write but not to read
        # must not see it fail here.
        return done


def abort_multipart(data: DataPlane, session: UploadSession) -> None:
    """Close an open upload and drop its parts. An upload already gone —
    aborted, completed, swept — is not an error: the parts are gone too."""
    try:
        data.multipart_upload.abort_multipart_upload(_abort_request(session))
    except ConnectError as err:
        if not _gone(err):
            raise


async def abegin_multipart(
    data: AsyncDataPlane,
    *,
    parent: str,
    content_type: str,
    size: int,
    key: str = "",
    metadata: dict[str, str] | None = None,
    tags: dict[str, str] | None = None,
) -> UploadSession:
    """``begin_multipart`` for the async clients."""
    session = _session(
        await data.multipart_upload.initiate_multipart_upload(
            _initiate_request(parent, key, content_type, size, metadata, tags)
        )
    )
    if not _split(session):
        await aabort_multipart(data, session)
        raise NoPartSplitError(f"no part size for {session.object_name}")
    return session


async def apresign_part(
    data: AsyncDataPlane, session: UploadSession, number: int, checksum: str
) -> resource_pb2.PresignedUrl:
    """``presign_part`` for the async clients."""
    with _own_keys():
        resp = await data.multipart_upload.presign_part(_presign_request(session, number, checksum))
    if not resp.upload_url.url:
        raise TransferError(_PUT, "", 0, _NO_URL)
    return resp.upload_url


async def acomplete_multipart(
    data: AsyncDataPlane, session: UploadSession, parts: Iterable[types_pb2.CompletedPart]
) -> types_pb2.Object:
    """``complete_multipart`` for the async clients."""
    done = await data.multipart_upload.complete_multipart_upload(_complete_request(session, parts))
    if done.collection:
        return done
    try:
        return await data.object.get_object(
            object_service_pb2.GetObjectRequest(name=session.object_name)
        )
    except ConnectError:
        return done


async def aabort_multipart(data: AsyncDataPlane, session: UploadSession) -> None:
    """``abort_multipart`` for the async clients."""
    try:
        await data.multipart_upload.abort_multipart_upload(_abort_request(session))
    except ConnectError as err:
        if not _gone(err):
            raise
