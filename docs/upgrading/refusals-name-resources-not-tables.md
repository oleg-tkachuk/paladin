## Unreleased — refused deletes name resources, not tables

Codes and reasons are unchanged; the message text moved. The table and the
constraint behind each refusal are in the server's RPC log line instead.

- `PurgeTenant` refused by what the tenant still owns reads `tenant still owns
  resources; remove them before purging it: held by users` (or `collections`,
  `owned buckets`, `billing charges`, …). It used to name the table, as in
  `tenant still has rows referencing it; …: users`.
- `DeleteBucket` names what holds the bucket the same way:
  `… referenced by 1 tenant default bindings` rather than
  `1 tenant_default_bindings`.
- A storage migration's recorded error, which `GetTenantStorageMigration`
  returns, no longer carries the database's or the storage backend's error. A
  refused rebind reads `rebind refused: collection "photos" cannot move to the
  dedicated bucket`; a failed physical verify names the object and the problem,
  as in `physical verify: docs/a.txt missing in the target bucket`.
- A purge refused by a legal hold or a retention window still names the object
  version holding it, as before.
