## Unreleased — the MCP presign tools bound `ttl_seconds`

- `paladin_presign_download`, `paladin_regenerate_upload_url` and
  `paladin_presign_part` refuse a `ttl_seconds` above 9223372036 as
  `invalid_argument: ttl_seconds: <value> is over the limit of 9223372036`,
  before the data plane is called. It used to wrap, and the plane refused a
  TTL the agent did not send.
