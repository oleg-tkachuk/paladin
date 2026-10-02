"""A large object streamed both ways, never held in memory whole.

Upload reads a file — or a pipe — once, front to back. download_stream hands
back a file-like reader a parser can consume; reading to the end verifies the
content against the checksum the upload recorded.
"""

from __future__ import annotations

import io

import paladin
from paladin.testing import FakePaladin

SIZE = 1 << 20


def main(fake: FakePaladin) -> bool:
    p = fake.connect()
    source = io.BytesIO(b"a large PDF " * (SIZE // 12))  # an open file, in practice
    size = len(source.getvalue())
    paladin.upload(
        p.data,  # type: ignore[arg-type]
        parent=str(fake.collection()),
        key="big.pdf",
        content_type="application/pdf",
        body=source,
        size=size,
    )
    uri = paladin.ObjectURI(fake.collection(), "big.pdf")
    read = 0
    with paladin.download_uri(p.data, uri) as reader:  # type: ignore[arg-type]
        for chunk in reader.chunks():  # a parser reading reader, in practice
            read += len(chunk)
    return read == size


if __name__ == "__main__":
    with FakePaladin() as f:
        print(main(f))
