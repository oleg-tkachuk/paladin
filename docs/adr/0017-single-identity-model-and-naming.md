# ADR-0017: One identity model, one naming convention

- **Status:** Accepted 2026-08-20. Supersedes the implicit split that grew up
  between natural-composite and surrogate keys. Implementation lands with this
  ADR; the migration history is consolidated at the same time.

- **Context.** The schema had grown two incompatible notions of identity, and
  nothing recorded which one a new table should follow:

  ```
  buckets         PK (backend_id, bucket_name)   -- natural composite
  object_keys     PK (tenant_id, object_key)     -- natural composite
  objects         PK (object_id)                 -- surrogate uuid
  object_versions PK (version_id)                -- surrogate uuid
  ```

  The composite half propagated. `quotas`, `replication_state`,
  `tenant_default_bindings` and `object_keys` each carried two columns per
  bucket reference; `objects` carried two more per collection reference. Every
  join gained a condition, every index gained a column, and renaming a bucket
  meant cascading a value that was also a key.

  Primary-key *naming* had split down the middle too — seven tables used `id`
  (`api_tokens`, `charges`, `capability_records`, …) and the rest used
  `<entity>_id` (`objects.object_id`, `operations.operation_id`,
  `event_subscriptions.subscription_id`, …). Neither was wrong; having both was.

  Three columns with near-identical names carried different meanings in the
  same struct:

  ```
  object_key  -- NOT a key: the container that holds objects
  key         -- the actual key (path within the container)
  s3_key      -- "<tenant>/<object_key>/<key>", composed for the backend
  ```

- **Decision.**

  **1. Surrogate identity everywhere.** Every table has `id uuid PRIMARY KEY`.
  Natural uniqueness is preserved as a `UNIQUE` constraint, not as identity:

  ```sql
  CREATE TABLE buckets (
      id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
      backend_id uuid NOT NULL REFERENCES storage_backends(id),
      name       text NOT NULL,
      UNIQUE (backend_id, name)
  );
  ```

  A rename now touches one row and no foreign keys.

  **2. `id` is the primary key's name, always.** Never `object_id`,
  `version_id`, `operation_id`. A column named `<entity>_id` is a *foreign*
  key to `<entity>.id` and nothing else. This makes the direction of every
  reference readable without consulting the schema.

  Exactly one exception, and the reason it is one: `oauth_clients.client_id`
  stays, as `text UNIQUE` alongside the `id uuid` primary key. It is a public
  OAuth 2.0 protocol identifier that appears in tokens and in client
  configuration — an external contract we do not get to choose.

  `storage_backends` was the other candidate and is deliberately NOT an
  exception. Its id was `text` holding `primary` / `secondary`, mirrored from
  `storage.backends.*` in config at bootstrap. That is a configuration name,
  not an identity, so it becomes `name text UNIQUE` beside `id uuid`, and
  bootstrap resolves name → id. The cost is real and accepted: `backend_id` in
  a log line or a dump is now a uuid rather than the word `primary`, so
  operational queries join to `storage_backends` for the name. The gain is
  that no table has a second kind of key.

  **3. The container is a `collection`.** `object_keys` → `collections`,
  addressed by `collection_id`. Inside it, `objects.key` → `objects.path`, and
  the composed backend location `s3_key` → `storage_path`. Three ambiguous
  names become three unambiguous ones, and the one that reads as a key no
  longer denotes a container.

  **4. `tenant_id` stays denormalised on every tenant-scoped table.** This is
  deliberate duplication, not a leftover. RLS policies read the tenant
  directly:

  ```sql
  CREATE POLICY tenant_isolation ON objects
      FOR ALL USING (tenant_id = paladin_session_tenant_id());
  ```

  Reaching the tenant through `collection_id` would turn every policy into a
  correlated subquery on the hot path, and RLS is a primary isolation control
  here (not defence-in-depth), so it must stay cheap enough that nobody is
  tempted to bypass it. The redundancy is enforced: `objects` carries a
  composite foreign key `(tenant_id, collection_id)` so a row cannot claim a
  collection belonging to another tenant.

  **5. Column vocabulary.**

  | Kind | Form | Example |
  | --- | --- | --- |
  | Primary key | `id uuid` | `objects.id` |
  | Foreign key | `<table_singular>_id` | `collection_id` |
  | Natural name | `name`, unique within parent | `collections.name` |
  | Human label | `display_name` | `tenants.display_name` |
  | Timestamps | `<verb>_at timestamptz` | `created_at`, `committed_at` |
  | State | Postgres `ENUM`, never `text` + CHECK | `objects.state` |
  | Optimistic lock | `resource_version` | unchanged |

- **Consequences.**

  Every bucket and collection reference narrows from two columns to one.
  Fifteen RLS policies keep their shape, because `tenant_id` did not move.
  The wire contract changes: `object_key` disappears from the API in favour of
  `collection`, and resource names become
  `tenants/{tenant}/collections/{collection}/objects/{object}`. Pre-1.0 and a
  dev-only deployment make that acceptable without a compatibility window
  (Constitution IV); there is no dual-read path and no aliasing.

  The 65-migration history is consolidated into a single baseline. The
  intermediate states are recoverable from git; carrying them forward would
  mean maintaining transformation migrations that no database will ever run
  twice, since every deployment reprovisions.

  Object Lock moves out of `objects` and `object_versions` into its own
  `object_locks` table, keyed by version. It was duplicated across both tables
  as three columns each (`lock_mode`, `lock_retain_until`, `legal_hold`), with
  `lock_mode` typed as `text` plus a CHECK. S3 semantics attach a lock to a
  version, so the version is the correct owner; the retention check on the
  delete path pays one join for a table that is empty for most deployments.

  What this does NOT change: RLS as the primary isolation control, the
  transactional outbox, presigned-URL delivery, or the capability module's
  independence from the database.
