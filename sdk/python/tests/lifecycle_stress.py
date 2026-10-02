"""One process that makes and drops SDK clients and transfers — sync and
async, plaintext and mutual TLS — and then exits, as a worker's test run does.
tests/test_lifecycle.py runs it many times and reads how each run ended.

Usage: python lifecycle_stress.py <rounds>
"""

from __future__ import annotations

import asyncio
import sys
import tempfile
from pathlib import Path

from certs import Authority, write
from tls_server import serve

import paladin
from paladin.common.v1 import resource_pb2
from paladin.iam.v1 import health_service_pb2
from paladin.testing import FakePaladin

BODY = b"x" * 1024
PUT = "PUT"


def _plaintext(fake: FakePaladin, rounds: int) -> None:
    parent = str(fake.collection())
    for i in range(rounds):
        p = fake.connect(transfer=paladin.Transfer())
        obj = paladin.upload(
            p.data, parent=parent, key=f"k{i}", content_type="text/plain", body=BODY, size=len(BODY)
        )
        assert paladin.download(p.data, obj.name) == BODY
        # The shared default Transfer, made on first use.
        shared = fake.connect()
        paladin.upload(
            shared.data,
            parent=parent,
            key=f"d{i}",
            content_type="text/plain",
            body=BODY,
            size=len(BODY),
        )

        async def run(i: int = i) -> None:
            ap = paladin.connect_async(
                paladin.Endpoints(data=fake.url), transfer=paladin.Transfer()
            )
            o = await paladin.aupload(
                ap.data,
                parent=parent,
                key=f"a{i}",
                content_type="text/plain",
                body=BODY,
                size=len(BODY),
            )
            assert await paladin.adownload(ap.data, o.name) == BODY

        asyncio.run(run())


def _tls(tmp: Path, rounds: int) -> None:
    ca = Authority()
    srv = serve(tmp, ca, ca.issue(client=False), h2=True)
    try:
        f = write(tmp, ca.pem, ca.issue(client=True, ips=False))
        tls = paladin.TLS(ca_file=f.ca, cert_file=f.cert, key_file=f.key)
        signed = resource_pb2.PresignedUrl(url=f"{srv.url}/bucket/key")
        for _ in range(rounds):
            p = paladin.connect(paladin.Endpoints(iam=srv.url), tls=tls)
            p.iam.health.get_version(health_service_pb2.GetVersionRequest())
            with paladin.Transfer(tls=tls).stream(PUT, signed, content=BODY) as resp:
                assert resp.status == 200

            async def run() -> None:
                ap = paladin.connect_async(paladin.Endpoints(iam=srv.url), tls=tls)
                await ap.iam.health.get_version(health_service_pb2.GetVersionRequest())
                async with paladin.Transfer(tls=tls).astream(PUT, signed, content=BODY) as r:
                    assert r.status == 200

            asyncio.run(run())
    finally:
        srv.stop()


def main(rounds: int) -> None:
    with FakePaladin() as fake:
        _plaintext(fake, rounds)
    with tempfile.TemporaryDirectory() as tmp:
        _tls(Path(tmp), rounds)


if __name__ == "__main__":
    main(int(sys.argv[1]))
