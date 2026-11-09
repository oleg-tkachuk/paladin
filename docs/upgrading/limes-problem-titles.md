## Unreleased — capability refusals say the library's title, not its words

- A refusal from the capability library now carries the fixed title of its
  problem as its message, such as "The token does not allow this request",
  instead of the library's own text. That text named resources, capability
  ids, audiences and decoding errors, and limes keeps it for the server's log
  (limes `docs/errors.md`).
- Branch on what stays: the Connect code, the ErrorInfo reason and the limes
  code in its metadata (`paladin.LimesCode`, `paladin.errors.limes_code`).
  A client that matched the old message text must move to them.
- Both SDKs' fakes answer with the same titles, held to the server by
  `sdk/testdata/*.json`.
