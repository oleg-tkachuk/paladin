"""A multipart upload interrupted part-way, resumed from what storage holds.

ListParts says which parts storage already has; only the rest are sent, and
the upload is completed with all of them. Keep the object name and the
upload id where a restart can find them.
"""

from __future__ import annotations

import paladin
from paladin.data.v1 import multipart_service_pb2, types_pb2
from paladin.testing import PART_SIZE, FakePaladin


def send_part(
    p: paladin.Paladin, transfer: paladin.Transfer, name: str, upload_id: str, n: int, data: bytes
) -> None:
    signed = p.data.multipart_upload.presign_part(  # type: ignore[union-attr]
        multipart_service_pb2.PresignPartRequest(
            object_name=name, upload_id=upload_id, part_number=n
        )
    )
    with transfer.stream("PUT", signed.upload_url, {"Content-Length": str(len(data))}, data):
        pass


def main(fake: FakePaladin) -> tuple[int, int, bool]:
    transfer = paladin.Transfer()
    p = fake.connect(transfer=transfer)
    body = b"x" * (2 * PART_SIZE + 10)
    init = p.data.multipart_upload.initiate_multipart_upload(  # type: ignore[union-attr]
        multipart_service_pb2.InitiateMultipartUploadRequest(
            parent=str(fake.collection()), key="big.bin", size_bytes=len(body)
        )
    )
    name, upload_id, size = init.object.name, init.upload_id, init.recommended_part_size

    def part(n: int) -> bytes:
        return body[(n - 1) * size : n * size]

    send_part(p, transfer, name, upload_id, 1, part(1))  # … and then the process died.

    # On restart, with name and upload_id kept from before:
    have = {
        pt.part_number
        for pt in p.data.multipart_upload.list_parts(  # type: ignore[union-attr]
            multipart_service_pb2.ListPartsRequest(object_name=name, upload_id=upload_id)
        ).parts
    }
    count = -(-len(body) // size)
    for n in range(1, count + 1):
        if n not in have:
            send_part(p, transfer, name, upload_id, n, part(n))
    listed = p.data.multipart_upload.list_parts(  # type: ignore[union-attr]
        multipart_service_pb2.ListPartsRequest(object_name=name, upload_id=upload_id)
    ).parts
    done = p.data.multipart_upload.complete_multipart_upload(  # type: ignore[union-attr]
        multipart_service_pb2.CompleteMultipartUploadRequest(
            object_name=name,
            upload_id=upload_id,
            parts=[
                types_pb2.CompletedPart(part_number=pt.part_number, etag=pt.etag) for pt in listed
            ],
        )
    )
    return len(have), len(listed), done.size_bytes == len(body)


if __name__ == "__main__":
    with FakePaladin() as f:
        print(main(f))
