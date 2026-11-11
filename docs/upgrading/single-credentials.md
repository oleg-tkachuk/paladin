## Unreleased — a credential presented twice is refused

- The data, IAM and admin planes now answer `INVALID_ARGUMENT` to a call that
  carries more than one `Authorization` header or more than one `DPoP` proof
  (RFC 6750 §2, RFC 9449 §4.3, §7.2). Before, each reader took the first
  value. A client that sends one of each is unaffected.
