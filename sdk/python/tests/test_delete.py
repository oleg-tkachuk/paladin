"""``delete`` supplies the resource_version every delete needs, rereads an
object that changed under it, and treats one already gone as done."""

from __future__ import annotations

import asyncio
from collections.abc import Iterator

import pytest
from connectrpc.code import Code

import paladin
from paladin import FailedPreconditionError, NotFoundError, VersionConflictError
from paladin.common.v1 import error_reason_pb2
from paladin.data.v1 import object_service_pb2
from paladin.testing import FakePaladin

CONTENT_TYPE = "text/plain"
DELETE = "/paladin.data.v1.ObjectService/DeleteObject"
GET = "/paladin.data.v1.ObjectService/GetObject"


@pytest.fixture
def fake() -> Iterator[FakePaladin]:
    with FakePaladin() as f:
        yield f


@pytest.mark.parametrize("permanent", [False, True])
def test_delete_removes_the_object_once(fake: FakePaladin, permanent: bool) -> None:
    data = fake.connect().data
    obj = fake.put(fake.collection(), "", CONTENT_TYPE, b"x")
    assert paladin.delete(data, obj.name, permanent=permanent)
    with pytest.raises(NotFoundError):
        data.object.get_object(object_service_pb2.GetObjectRequest(name=obj.name))  # type: ignore[union-attr]
    assert not paladin.delete(data, obj.name, permanent=permanent), "already gone is not an error"


def test_delete_rereads_a_changed_object(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "", CONTENT_TYPE, b"x")
    fake.fail_rpc(DELETE, 1, Code.ABORTED)
    assert paladin.delete(fake.connect().data, obj.name, permanent=True)
    assert len(fake.calls(GET)) == 2


def test_delete_gives_up_on_a_persistent_conflict(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "", CONTENT_TYPE, b"x")
    fake.fail_rpc(DELETE, paladin.DELETE_ATTEMPTS, Code.ABORTED)
    with pytest.raises(VersionConflictError):
        paladin.delete(fake.connect().data, obj.name)
    assert len(fake.calls(GET)) == paladin.DELETE_ATTEMPTS


def test_delete_raises_any_other_refusal(fake: FakePaladin) -> None:
    obj = fake.put(fake.public_collection(), "", "image/jpeg", b"x")
    with pytest.raises(FailedPreconditionError) as caught:
        paladin.delete(fake.connect().data, obj.name)
    assert caught.value.reason == error_reason_pb2.ERROR_REASON_PUBLIC_COLLECTION_RULE
    assert len(fake.calls(GET)) == 1


def test_adelete(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "", CONTENT_TYPE, b"x")

    async def run() -> tuple[bool, bool]:
        p = paladin.connect_async(paladin.Endpoints(data=fake.url, admin=fake.url, iam=fake.url))
        assert p.data is not None
        return (
            await paladin.adelete(p.data, obj.name, permanent=True),
            await paladin.adelete(p.data, obj.name, permanent=True),
        )

    assert asyncio.run(run()) == (True, False)


def test_delete_of_an_object_that_went_meanwhile(fake: FakePaladin) -> None:
    obj = fake.put(fake.collection(), "", CONTENT_TYPE, b"x")
    fake.fail_rpc(DELETE, 1, Code.NOT_FOUND)
    assert not paladin.delete(fake.connect().data, obj.name)
