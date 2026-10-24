"""The server names its release on every response, an error included, and the
SDK carries it on the PaladinError, so a consumer's code that reports a
contract skew reads it there. paladin.testing names its own."""

from __future__ import annotations

import pytest

from paladin import PaladinError
from paladin.data.v1 import object_service_pb2
from paladin.testing import SERVER_VERSION, FakePaladin

_MISSING_OBJECT = "00000000-0000-4000-8000-000000000000"


def test_an_error_names_the_fakes_release() -> None:
    with FakePaladin() as fake:
        data = fake.connect().data
        with pytest.raises(PaladinError) as err:
            data.object.get_object(
                object_service_pb2.GetObjectRequest(
                    name=f"{fake.collection()}/objects/{_MISSING_OBJECT}"
                )
            )
    assert err.value.server_version == SERVER_VERSION
