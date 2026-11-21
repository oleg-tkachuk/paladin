## Unreleased — version reads that would answer wrongly fail instead

- `GetObjectVersion` whose current-version pointer cannot be read answers
  `INTERNAL`, where it answered the version with `is_current: false`.
- `RestoreObject` on a versioned collection whose versioning config or
  current version cannot be read answers `INTERNAL`, where it restored the
  object and left its current version on the delete marker.
