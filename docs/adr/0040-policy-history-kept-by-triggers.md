# ADR-0040: A policy's history is kept by triggers, with the writer the pool names

- **Status:** Accepted. Implemented 2026-10-27.
- **Related:** row-level security from the schema baseline (`002`, ADR-0016);
  the audit log's before/after images (`099`).

- **Context.** A Cedar policy is stored in place, on its storage backend,
  bucket, collection or tenant. A write left no trace of the text it
  replaced, so when a change started refusing requests nobody could say
  what it had been, who changed it or when, and the console could diff a
  draft only against what was saved. The audit log's before/after images
  are the nearest thing, but they are read outside the write's transaction,
  cover admin RPCs only, and age out with the log.

- **Decision.**
  1. **`policy_versions` holds every text each layer has held**: the
     layer, its owning row, the text, a per-layer version number, who wrote
     it and when. Migration `101` seeds version 1 from every layer that
     already held a policy.
  2. **Triggers write it, not the handlers.** `AFTER INSERT` and
     `AFTER UPDATE OF <policy column>` on the four tables append a version
     when the text changes — a cleared policy included — so a tenant's
     default policy at creation and an operator's `psql` are recorded like
     an RPC. `AFTER DELETE` drops a row's history with it, so a purged
     tenant leaves none behind.
  3. **The writer comes from the session.** The pool sets `paladin.actor`,
     the principal's subject, on every acquire, as it sets
     `paladin.tenant_id`, and clears it on release. A write no request made
     records no writer (NULL), not a placeholder.
  4. **History is read-only to the application.** The table has a SELECT
     policy only — a collection's or tenant's versions under the same
     isolation as its collections, a backend's or bucket's for anyone the
     RPC admits — and the triggers write as their SECURITY DEFINER owner.
  5. **`PolicyService.ListPolicyVersions` serves it**, gated as
     `GetEffectivePolicy` is, since both return policy text.

- **Consequences.**
  - Every policy change is attributable, and any two versions can be
    compared.
  - A version is per layer, not per effective stack: what a resource was
    authorised under at a time is the versions of its layers then.
  - The writer is a subject (a user's UUID, an agent's `agent:…`) and the
    writer's own tenant (`paladin.actor_tenant`, migration `102`), since a
    subject names a user only within its tenant and a platform admin edits
    other tenants' policies. Showing a name is the reader's join.
