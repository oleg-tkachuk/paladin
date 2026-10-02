# ADR-0019: Object search under RLS, and an opt-in read replica

- **Status:** Accepted 2026-10-02.

- **Context.** Searching a collection's object metadata — key substring, tag
  and metadata equality, content type — was a CEL filter that Postgres saw
  only partly: state and a key LIKE without metacharacters were pushed into
  the query, everything else was evaluated in Go over whatever page the
  query returned. A selective tag filter therefore read the tenant 500 rows
  at a time to find a handful, and failed outright (`no such key`) when a
  fetched row lacked the tag. A key containing `_` pushed nothing at all.

  Dedicated search engines were considered (OpenSearch, Elasticsearch,
  Qdrant, Weaviate, Milvus, Bleve) and rejected for now. The searchable data
  is a few short structured fields per object — Paladin never sees object
  bytes — and Postgres is both the source of truth and the tenant-isolation
  boundary (RLS). An external index would be a second copy, eventually
  consistent, with tenant isolation reimplemented outside the database. That
  trade starts to pay at hundreds of millions of objects or with content
  search; neither is the case.

  Indexing inside Postgres met one obstacle. Under RLS the planner may use a
  query's own predicate before the policy's only when its operator is
  LEAKPROOF, and jsonb `@>` and `LIKE` are not. For the runtime role the new
  GIN and trigram indexes were simply not in the plan: at 200k objects in a
  tenant a tag filter took 96 ms under RLS against 1.7 ms without, and it
  grows linearly.

- **Decision.**

  1. **Push the whole indexable subset down.** `cel.ExtractObjectPushdown`
     also recognises `content_type` equality and prefix and
     `tags[k] == v` / `metadata[k] == v`; LIKE literals are escaped rather
     than dropped. The CEL program stays authoritative over every fetched
     row, as before: a hint only narrows. No API or filter-grammar change.
  2. **Indexes** (migrations 025–028, 030–031): `pg_trgm` on
     `objects.path`, `jsonb_path_ops` GIN on `tags` and `metadata`; and the
     keyset index rebuilt as `(tenant_id, collection_id, id) INCLUDE (state)`,
     so a status-filtered page (the console's status dropdown, the trash
     view) is an index-only scan. It replaces `idx_objects_keyset` rather
     than joining it, so uploads maintain no extra B-tree. Considered and left
     out: a prefix index on `path` (no client sends `key.startsWith`, and the
     trigram index already serves it), `content_type` in the keyset index
     (no console filter uses it), extended statistics on
     `(tenant_id, collection_id)` (no plan changed for the better once the
     collection is resolved up front).
  3. **Candidate ids from a SECURITY DEFINER function** (029,
     `search_object_ids`). It runs as the migrate role (BYPASSRLS), so the
     indexes are usable, and returns **ids only**. `ListObjects` reads the
     rows through `id = ANY(...)` under the caller's RLS, so the policy still
     decides every row a client receives; the function only decides which
     rows are looked at. It refuses to search a tenant other than the
     session's, using the policy's own `paladin_session_tenant_id()` /
     `paladin_session_cross_tenant()`, so neither its result nor its timing
     says anything about another tenant. EXECUTE is revoked from PUBLIC and
     granted to `paladin_app`. The query inside is built from fixed fragments
     with every value passed through `USING`, so each call is planned for the
     predicates it actually has. The collection is resolved to its id before
     that query, and `ListObjects` reads the returned rows without joining
     `collections`: both joins misled the planner into scanning or sorting
     the whole collection per page.

     Rejected: marking `jsonb_contains`/`textlike` LEAKPROOF (superuser-only,
     unavailable on managed Postgres, and not true of `textlike`, whose
     errors depend on its argument); leaving the indexes unused.
  4. **An opt-in read replica** (`datastores.postgres.replica`, off by
     default) for the same lag-tolerant reads: listing, counting, tag facets.
     Authorization and read-after-write paths never use it. A lag probe
     (`ReadRouter`) routes reads to the replica only while it is within
     `max_lag`, falls back to the primary when it is behind, unreachable or
     of unknown lag, and a query the replica fails is retried on the primary.
     On CloudNativePG `enabled: true` is the whole configuration: the DSN is
     derived from the primary's (`<cluster>-rw` → `<cluster>-ro`), and CNPG
     seeds and streams the standbys itself (ADR-0005).

- **Consequences.**
  - Measured at 200k objects per tenant, under RLS: tag equality 96 → 3 ms,
    key substring 64 → 10 ms. At 1M objects (40 tenants × 5 collections,
    UUIDv7 ids), a page of 500: no filter 4 ms, status filter 4 ms, tag 2–3
    ms, key substring 3 ms, content type 14 ms. A filter matching few objects now returns them
    on the first page instead of an empty page with a cursor, and a filtered
    `CountObjects` is exact far more often (its scan cap counts rows read).
  - The tenant check exists in one more place, though it calls the policy's
    functions rather than restating them, and a mistake there can make a
    search slower or emptier but cannot return another tenant's row.
    `object_search_test.go` pins the scoping, the PUBLIC revoke and the
    plans; the statements it explains must be kept in step with 029.
  - A role used as the runtime DSN needs EXECUTE on `search_object_ids`;
    `paladin_app` has it. Deployments whose migrate role lacks BYPASSRLS
    still return correct results, without the index speed-up.
  - With the replica on, a list may trail a write by up to `max_lag`.
  - Revisit with a dedicated engine if search must cover object content, or
    a single tenant approaches hundreds of millions of objects.
