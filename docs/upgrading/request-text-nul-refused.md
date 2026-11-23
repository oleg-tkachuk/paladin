## Unreleased — request text holding NUL is refused on every RPC

- Every RPC on the data, admin and IAM planes refuses a request whose
  strings hold a NUL character (U+0000) as `INVALID_ARGUMENT`, before any
  handler runs. The message names the field and never its value:
  `key: holds a NUL character`, `metadata.value: holds a NUL character`,
  `names[2]: holds a NUL character`. A map entry is named by its half —
  `tags.key` or `tags.value` — not by its key.
- Every string counts: singular and repeated fields, map keys and values,
  nested messages, oneofs and well-known types such as `StringValue` and
  `Struct` (user settings' `preferences`). `bytes` fields are not checked.
- Such text could not be stored: PostgreSQL holds no NUL in `text` and refuses
  `\u0000` in `jsonb`. A call carrying it could fail at the store and come
  back as `INTERNAL` — a `Login` whose subject held one did. Fields a handler
  already checked, such as an object key, keep their code but now answer
  with this message.
- The refusal comes after authentication: a call without valid credentials is
  still `UNAUTHENTICATED` first. `Login` takes no credentials, so a subject
  holding NUL is refused, and not recorded in the audit log.
- A client that sends such text deliberately should strip or replace it
  before the call; there is no flag to accept it.
