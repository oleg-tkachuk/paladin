## Unreleased — inputs the server misread are refused

- A checksum must be its digest's one base64 spelling. A value with a line
  break, or with unused trailing bits set (`AAAAAB==` for `AAAAAA==`), is
  `INVALID_ARGUMENT` wherever a checksum is declared; it used to pass and
  then fail the comparison with the store's checksum.
- A canonical collection name whose backend or bucket holds a `/`
  (`storageBackends/a/b/buckets/…`) is `INVALID_ARGUMENT`. No backend or
  bucket can be addressed by such a name elsewhere in the API.
- A user settings name with anything after `/settings`
  (`users/{id}/settings/x`) is `INVALID_ARGUMENT`; it used to address
  `users/{id}`.
- `ListObjectVersions` with a malformed `page_token` is `INVALID_ARGUMENT`,
  as every other list is, not `INTERNAL`.
- A list filter whose evaluation costs more than the server's bound — nested
  comprehensions over literal lists, say — is `INVALID_ARGUMENT` on the list
  RPCs, matches nothing on `ListObjects`, and skips the row in subscriptions
  and lifecycle rules. Filters of ordinary shape stay orders of magnitude
  below it.
- A `trusted_proxies` entry written with an IPv6 zone (`fe80::1%eth0`) is
  refused when a role builds its listener, and the role does not start. Such
  an entry never trusted anything; write the address without its zone.
