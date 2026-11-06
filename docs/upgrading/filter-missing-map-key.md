## Unreleased — a filter on a missing map key no longer fails

- In every CEL filter and lifecycle rule, `labels`, `tags` and `metadata`
  read a key they do not hold as `""`, as a NULL column reads. A filter such
  as `tags["env"] == "prod"` used to fail the whole list with
  `INVALID_ARGUMENT` ("no such key") whenever one row on the page lacked
  `env`; it now matches the rows that hold the pair.
- `has(tags.env)` and `"env" in tags` still test whether the key is
  present. `tags["env"] == ""` matches a missing key and a key set to `""`
  alike; tell them apart with `has`.
