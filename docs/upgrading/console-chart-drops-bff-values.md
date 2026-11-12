## Unreleased — the console chart drops what its BFF read

- The console chart no longer takes `healthSnapshotTokenSecret`,
  `backend.healthRoles`, or `backend.urls` other than `iam`; an overlay still
  setting any of them fails schema validation. Remove them: the console's
  server reaches only the iam plane now, for the OAuth consent relay
  (ADR-0048).
- The console's NetworkPolicy reaches the iam pods alone, on the iam port, and
  only the backend chart's iam role admits the console's pods.
- `session.service.name` defaults to the backend release's session Service,
  `<fullname>-session`, built like the iam URL from `backend.release`.
- `values-prod.yaml` drops `service.stickySessions`: nothing needs a browser
  pinned to one console replica since the session moved to the session role.
