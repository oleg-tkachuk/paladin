# ADR-0028: A bucket row is a claim — Paladin registers only what it means to manage

- **Status:** Accepted — implemented 2026-10-07.
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (buckets and their
  binding), [ADR-0027](0027-public-collections.md) (public buckets).

- **Context.** A bucket row says Paladin manages that physical bucket: it sets
  its policy — anonymous read for a public bucket — lifecycle rules,
  versioning, and may delete it on the backend. Registration did not match
  that weight. `provision_on_backend` took any bucket the backend already
  held as created: `BucketAlreadyOwnedByYou` and even `BucketAlreadyExists`,
  a name another account owns, were success. So a row could be written for
  the backend's own configured bucket, which holds data the registry knows
  nothing of, and a public one would have had anonymous reads set over all
  of it. `EnsureTenantStorage` let a tenant take any existing bucket the same
  way, and a delete on the backend could reach a bucket Paladin never made.

- **Decision.**
  1. **Create and adopt are different requests.** `provision_on_backend`
     true creates: the handler asks the backend first (`HeadBucket`) and
     refuses a bucket it holds — `ALREADY_EXISTS`,
     `BUCKET_EXISTS_ON_BACKEND`. False adopts an existing bucket and refuses
     one the backend does not hold — `BUCKET_NOT_ON_BACKEND`. A bucket the
     backend answers for but refuses to us exists all the same.
  2. **Paladin's own buckets are never registered:** each backend's
     configured `bucket` and the feature probe's scratch buckets —
     `BUCKET_RESERVED`.
  3. **A row records whether Paladin creates its bucket**
     (`created_on_backend`), set as the row is written and never changed.
     Only such a bucket is deleted on the backend; an adopted one keeps its
     data — `BUCKET_NOT_CREATED_BY_PALADIN`. A public bucket is always one
     Paladin creates, so it is always empty when its policy is set.
  4. **Self-service creates, never adopts.** `EnsureTenantStorage` leaves an
     existing row alone and creates a missing bucket, but refuses one the
     backend holds unregistered: adoption is an operator's decision.
  5. **The store's answer is typed.** The adapter reads the SDK's
     `BucketAlreadyOwnedByYou` — the reconciler's own earlier attempt — as
     success and `BucketAlreadyExists` as `ErrBucketOwnedElsewhere`.

- **Consequences.** A consumer whose bucket exists but was never registered
  is refused by `EnsureTenantStorage` until an operator adopts it. Rows that
  predate the column count as adopted: Paladin no longer deletes their
  buckets on the backend, which errs on keeping data. Registering a bucket
  without provisioning needs the backend reachable, as provisioning did.

- **Alternatives considered.** Keeping silent adoption and only refusing a
  public bucket over an existing one: closes the exposure, but a row would
  still claim management of a bucket nobody decided to hand over, and a
  delete on the backend would still reach it.
