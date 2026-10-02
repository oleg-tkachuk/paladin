"""A token operators rotate through a UI, read from its store on every call.

A token source is asked for the token on every call, so a rotation takes
effect on the next one with no new client. With an ``invalidate`` method, a
call the server refuses as unauthenticated is made once more with a fresh
token — for the window where a token was rotated mid-call.
"""

from __future__ import annotations

import threading

import paladin


class SettingsStore:
    """Stands for wherever the token lives — a database row, a vault secret."""

    def __init__(self, token: str) -> None:
        self._token = token
        self._lock = threading.Lock()

    def get(self) -> str:
        with self._lock:
            return self._token

    def rotate(self, token: str) -> None:
        with self._lock:
            self._token = token


class StoreToken:
    """A ``paladin`` token source over the store."""

    def __init__(self, store: SettingsStore) -> None:
        self._store = store

    def token(self, audience: str) -> str:
        return self._store.get()

    def invalidate(self, audience: str) -> None:
        """Nothing is cached, so there is nothing to drop: the retry reads again."""


def build(store: SettingsStore) -> paladin.Paladin:
    return paladin.connect(
        paladin.Endpoints(data="https://paladin-data.example.com"), token_source=StoreToken(store)
    )


if __name__ == "__main__":
    build(SettingsStore("paladin_pat_…"))
