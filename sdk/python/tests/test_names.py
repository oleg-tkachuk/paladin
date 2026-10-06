"""Resource names and object URIs, against sdk/testdata/names.json — the
cases the Go SDK's tests read too, so both accept, refuse and print alike."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from data_plane_fake import Fake

from paladin import (
    API_TOKEN_PREFIX,
    BucketName,
    CollectionName,
    Endpoints,
    InvalidNameError,
    ObjectName,
    ObjectURI,
    ObjectVersionName,
    TenantName,
    connect,
    download_uri,
    object_resource,
    upload,
)

SHARED = Path(__file__).resolve().parents[2] / "testdata" / "names.json"
TENANT = "0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00"


def _fields(kind: str, value: Any) -> dict[str, str]:
    if kind == "tenant":
        return {"tenant": value.tenant}
    if kind == "collection":
        return {"tenant": value.tenant, "collection": value.collection}
    if kind == "object":
        return {**_fields("collection", value.collection), "object": value.object}
    if kind == "version":
        return {**_fields("object", value.object), "version": value.version}
    return {**_fields("collection", value.collection), "key": value.key}


PARSERS = {
    "tenant": TenantName.parse,
    "collection": CollectionName.parse,
    "object": ObjectName.parse,
    "version": ObjectVersionName.parse,
    "uri": ObjectURI.parse,
}
CASES = [
    (kind, case)
    for kind, cases in json.loads(SHARED.read_text()).items()
    if not kind.startswith("_")
    for case in cases
]


def test_the_shared_cases_cover_every_parser() -> None:
    assert {kind for kind, _ in CASES} == set(PARSERS)


@pytest.mark.parametrize(("kind", "case"), CASES, ids=[f"{k}:{c['in']}" for k, c in CASES])
def test_names_against_the_shared_cases(kind: str, case: dict[str, Any]) -> None:
    parse = PARSERS[kind]
    if not case["ok"]:
        with pytest.raises(InvalidNameError):
            parse(case["in"])
        return
    got = parse(case["in"])
    want = {k: v for k, v in case.items() if k not in ("in", "ok", "out")}
    assert _fields(kind, got) == want
    assert str(got) == case.get("out", case["in"])
    assert parse(str(got)) == got


def test_download_uri_looks_the_object_up_by_key(fake: Fake) -> None:
    data = connect(Endpoints(data=fake.base)).data
    body = b"by key"
    upload(
        data,
        parent=f"tenants/{TENANT}/collections/c",
        key="k",
        content_type="text/plain",
        body=body,
        size=len(body),
    )
    uri = str(ObjectURI(CollectionName(TENANT, "c"), "k"))
    with download_uri(data, uri) as reader:
        assert reader.read() == body
    assert fake.looked_up == [f"tenants/{TENANT}/collections/c|k"]
    with pytest.raises(InvalidNameError):
        download_uri(data, "paladin://docs/k")


TENANT_ID = "7ba7b810-9dad-11d1-80b4-00c04fd430c8"


# A name built from parts was checked only by parse, so consumers validated by
# parse(str(Name(...))). The constructor now refuses what the server refuses,
# and keeps an id in its canonical form.
def test_collection_name_checks_its_parts() -> None:
    assert CollectionName(TENANT_ID.upper(), "docs").tenant == TENANT_ID
    with pytest.raises(InvalidNameError):
        CollectionName("acme", "docs")  # a slug, which the server refuses under a tenant
    with pytest.raises(InvalidNameError):
        CollectionName(TENANT_ID, "")


def test_object_and_uri_names_check_their_parts() -> None:
    collection = CollectionName(TENANT_ID, "docs")
    with pytest.raises(InvalidNameError):
        ObjectName(collection, "not-a-uuid")
    with pytest.raises(InvalidNameError):
        ObjectURI(collection, "")
    with pytest.raises(InvalidNameError):
        TenantName("a/b")


def test_bucket_name_round_trips() -> None:
    name = BucketName("primary", "paladin-shared")
    assert str(name) == "storageBackends/primary/buckets/paladin-shared"
    assert BucketName.parse(str(name)) == name


@pytest.mark.parametrize(
    "value",
    [
        "",
        "storageBackends/primary",
        "storageBackends//buckets/b",
        "storageBackends/p/buckets/",
        "buckets/b",
        "storageBackends/p/buckets/b/extra",
    ],
)
def test_bucket_name_refuses_other_shapes(value: str) -> None:
    with pytest.raises(InvalidNameError):
        BucketName.parse(value)


# The server checks a capability's resource against this exact shape; the
# consumers that grant on a key prefix spelled it by hand.
def test_object_resource() -> None:
    assert object_resource(TENANT_ID, "docs", "reports/") == f"object://{TENANT_ID}/docs/reports/"
    assert API_TOKEN_PREFIX == "paladin_pat_"
