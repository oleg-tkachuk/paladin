"""From a hand-written Connect-JSON client to the SDK.

Before: a POST of JSON to ``/<package>.<Service>/<Method>``, headers set by
hand — Authorization, Idempotency-Key, User-Agent — and errors read from the
response body's ``code``::

    resp = httpx.post(f"{data}/paladin.data.v1.ObjectService/GetObject",
                      json={"name": name}, headers={"Authorization": f"Bearer {token}"})
    if resp.status_code == 404 and resp.json()["code"] == "not_found": ...

After: the generated client's method, with the SDK's headers, retries and
typed errors.
"""

from __future__ import annotations

import paladin
from paladin.data.v1 import object_service_pb2
from paladin.testing import FakePaladin


def main(fake: FakePaladin) -> bool:
    p = fake.connect()
    missing = f"{fake.collection()}/objects/00000000-0000-4000-8000-000000000000"
    try:
        p.data.object.get_object(object_service_pb2.GetObjectRequest(name=missing))  # type: ignore[union-attr]
    except paladin.NotFoundError:
        return True
    return False


if __name__ == "__main__":
    with FakePaladin() as f:
        print(main(f))
