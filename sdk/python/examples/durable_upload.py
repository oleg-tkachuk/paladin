"""An upload kept until it completes: a failure part-way is tried again,
resuming the multipart session where it stopped.

``upload`` retries each request and completes the object itself. What it
leaves to the application is here: keeping the open session where a restart
finds it, and trying the whole upload again. A body is read once, so each
attempt opens it afresh.
"""

from __future__ import annotations

import io
import threading
import time
from collections.abc import Callable
from typing import IO

from connectrpc.code import Code

import paladin
from paladin.data.v1 import types_pb2
from paladin.facade import DataPlane
from paladin.testing import PART_SIZE, FakePaladin, StorageOp

# Codes a call may succeed on when made again: Paladin down, busy or slow.
TRANSIENT_CODES = frozenset({Code.UNAVAILABLE, Code.RESOURCE_EXHAUSTED, Code.DEADLINE_EXCEEDED})


class SessionStore:
    """Open multipart sessions, kept where a restart finds them: a table in
    the application's database, here a dict."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._sessions: dict[str, paladin.UploadSession] = {}

    def load(self, key: str) -> paladin.UploadSession | None:
        with self._lock:
            return self._sessions.get(key)

    def save(self, key: str, session: paladin.UploadSession) -> None:
        with self._lock:
            self._sessions[key] = session

    def delete(self, key: str) -> None:
        with self._lock:
            self._sessions.pop(key, None)


def transient(err: BaseException) -> bool:
    """Whether an upload that failed with ``err`` may succeed when tried
    again: storage busy or down, an expired URL, Paladin unavailable or busy,
    the network. Anything else — a refusal, a bad request — fails again."""
    if isinstance(err, paladin.TransferError):
        return err.status >= 500 or err.status == 429 or paladin.expired(err)
    if isinstance(err, paladin.PaladinError):
        return err.code in TRANSIENT_CODES
    return isinstance(err, OSError)


def upload_durably(
    data: DataPlane,
    store: SessionStore,
    *,
    parent: str,
    key: str,
    content_type: str,
    size: int,
    open_body: Callable[[], IO[bytes]],
    attempts: int = 5,
    multipart_threshold: int = paladin.DEFAULT_MULTIPART_THRESHOLD,
) -> tuple[types_pb2.Object, int]:
    """``upload`` until it completes, at most ``attempts`` times; returns the
    object and the attempts it took. A multipart session is saved as soon as
    it opens, so the next attempt — here or after a restart — sends only the
    parts storage does not hold; it is dropped once the object is complete."""
    store_key = f"{parent}/{key}"
    for attempt in range(1, attempts + 1):
        resume = store.load(store_key)
        try:
            with open_body() as body:
                obj = paladin.upload(
                    data,
                    parent=parent,
                    key=key,
                    content_type=content_type,
                    body=body,
                    size=size,
                    multipart_threshold=multipart_threshold,
                    on_session=lambda s: store.save(store_key, s),
                    resume=resume,
                )
        except Exception as err:
            if resume is not None and isinstance(err, paladin.NotFoundError):
                store.delete(store_key)  # the server swept the session: start over
            elif not transient(err) or attempt == attempts:
                raise
            time.sleep(0.01 * attempt)  # longer in production
            continue
        store.delete(store_key)
        return obj, attempt
    raise AssertionError("unreachable")


def main(fake: FakePaladin) -> tuple[int, bool, bool]:
    body = b"x" * (2 * PART_SIZE + 10)
    puts = 0

    def busy_once(op: StorageOp) -> tuple[int, str] | None:
        # Storage is busy for one part of the first attempt.
        nonlocal puts
        if op.method == "PUT":
            puts += 1
            if puts == 2:
                return 503, "busy"
        return None

    fake.fail_storage(busy_once)
    # One attempt per request, so the busy part fails the upload.
    data = fake.connect(transfer=paladin.Transfer(attempts=1)).data
    assert data is not None
    store = SessionStore()
    parent = str(fake.collection())
    obj, attempts = upload_durably(
        data,
        store,
        parent=parent,
        key="big.bin",
        content_type="application/octet-stream",
        size=len(body),
        open_body=lambda: io.BytesIO(body),  # open(path, "rb") for a file
        multipart_threshold=PART_SIZE,
    )
    return attempts, fake.content(obj.name) == body, store.load(f"{parent}/big.bin") is None


if __name__ == "__main__":
    with FakePaladin() as f:
        print(main(f))
