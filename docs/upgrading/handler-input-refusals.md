## Unreleased — what a client is told about its own input

- A list RPC refuses a filter that does not compile with one line ending in
  `<line>:<column>: <message>`, in place of the CEL report that echoed the
  expression over several lines. In that message and in `CELService.Validate`'s
  `message`, a character that does not print is escaped (`\x7f`).
- A Cedar parse error, from `PolicyService.Validate` and from every write that
  carries a policy, names its line and column in the text that was sent; it
  used to count from the start of the built-in layer. A character in it that
  does not print is escaped.
- A policy that does not type-check is still one `PolicyService.Validate`
  warning, with its errors joined by semicolons on one line rather than by line
  breaks.
- `PolicyService.SimulateAuthz` answers a resource name it cannot read as
  `INVALID_ARGUMENT`, and a caller it refuses as `PERMISSION_DENIED` or
  `UNAUTHENTICATED`; every refusal used to be `INTERNAL`. The explanation
  quotes the resource name and ends in `allow` or `deny` rather than `1` or
  `0`.
- An amount in a currency that is no ISO 4217 code is refused in words
  (`unit_code "x": not an ISO 4217 currency code`), not as a dump of a Go
  value.
- An unknown timezone in user settings is refused as
  `unknown timezone "<name>"`, quoted, without the loader's own text.
- A capability `ttl_seconds` above 9223372036 is `INVALID_ARGUMENT`. It used to
  wrap: refused as a negative TTL, or minted with the default lifetime.
- `CreateCollection` (and the storage bootstrap's collections) refuse a name
  outside the collection grammar as `INVALID_ARGUMENT` before the insert,
  whose CHECK constraint refused it instead.
- A tenant display name longer than 255 characters is `INVALID_ARGUMENT` on
  create and update, refused before the insert rather than by its CHECK
  constraint.
