## release/v0.2.0, sdk/python/v0.145.1, sdk/go/v0.145.1 — capability refusals say the library's title, not its words

- A refusal from the capability library now carries the fixed title of its
  problem as its message, such as "The token does not allow this request",
  instead of the library's own text. That text named resources, capability
  ids, audiences and decoding errors, and limes keeps it for the server's log
  (limes `docs/errors.md`).
- A refusal of the client's own input — `invalid_request`,
  `invalid_caveats`, `invalid_amount`, `delegation_too_wide` — adds after the
  title which part of the request was refused, as the library words it:
  "The delegation asks for more than its parent holds: an op is not among
  the parent's". The detail names a rule, never a value.
- Both fakes refuse a delegated op the parent lacks as the server does, with
  limes code `delegation_too_wide`, where they answered a message of their
  own without it.
- Branch on what stays: the Connect code, the ErrorInfo reason and the limes
  code in its metadata (`paladin.LimesCode`, `paladin.errors.limes_code`).
  A client that matched the old message text must move to them.
- Both SDKs' fakes answer with the same titles and details, held to the
  server by `sdk/testdata/*.json`.
