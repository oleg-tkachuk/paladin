"""Many documents in, a few at a time, each failure on its own.

The kind of loop an ingestion worker runs: every document of a batch fetched
at a bounded concurrency, one bad document reported without stopping the
rest. The async form is the same for a worker on asyncio.
"""

from __future__ import annotations

import asyncio

import paladin
from paladin.testing import FakePaladin

CONCURRENCY = 4


def ingest(data: object, names: list[str]) -> tuple[int, list[str]]:
    total, failed = 0, []
    for name, content in paladin.download_many(data, names, concurrency=CONCURRENCY):  # type: ignore[arg-type]
        if isinstance(content, BaseException):
            failed.append(name)
            continue
        total += len(content)  # hand content to a parser instead
    return total, failed


async def aingest(data: object, names: list[str]) -> int:
    total = 0
    async for _, content in paladin.adownload_many(data, names, concurrency=CONCURRENCY):  # type: ignore[arg-type]
        if not isinstance(content, BaseException):
            total += len(content)
    return total


def main(fake: FakePaladin) -> tuple[int, list[str], int]:
    names = [
        fake.put(fake.collection(), f"docs/{i}.txt", "text/plain", b"x" * i).name
        for i in range(1, 6)
    ]
    missing = f"{fake.collection()}/objects/00000000-0000-4000-8000-000000000000"
    total, failed = ingest(fake.connect().data, [*names, missing])
    async_total = asyncio.run(
        aingest(paladin.connect_async(paladin.Endpoints(data=fake.url)).data, names)
    )
    return total, failed, async_total


if __name__ == "__main__":
    with FakePaladin() as f:
        print(main(f))
