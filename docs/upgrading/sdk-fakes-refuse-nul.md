## sdk/python/v0.152.1, sdk/go/v0.152.1 — the SDK fakes refuse request text holding NUL

- `paladintest` (Go) and `paladin.testing` (Python) refuse a request any of
  whose strings holds a NUL character (U+0000) as `INVALID_ARGUMENT`, with the
  server's text: `key: holds a NUL character`, `metadata.value: holds a NUL
  character`. The refusal comes after authentication and before validation,
  as on the server, and no handler runs.
- A test that passed such text through either fake now fails as it would
  against the server.
