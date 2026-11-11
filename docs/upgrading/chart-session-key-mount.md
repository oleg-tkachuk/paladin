## Unreleased — the chart mounts a session signing key into the minting role

- The chart now provides the ECDSA P-256 key sessions will be signed with
  (ADR-0047) and mounts it as a file into `auth.sessionKey.roles` (`api`)
  only, at `auth.sessionKey.mountPath`. No Role grants reading it.
- Under ArgoCD or any client-side render, provide the Secret yourself and set
  `auth.sessionKey.existingSecret`: the chart's generated key would change on
  every sync. The key may be PEM PKCS#8 (`openssl genpkey -algorithm EC
  -pkeyopt ec_paramgen_curve:P-256`) or SEC1.
- This release mounts the key and nothing reads it yet; the release that
  switches sessions to ES256 reads it from there.
