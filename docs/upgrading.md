# Upgrading

## Changing the API contract

`buf breaking` runs in `verify-all`, and so in CI, against the tag
pinned as `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh`, and
**blocks**. The contract is pre-1.0 (`api/v0.x`) and still breaks
compatibility deliberately, so the gate does not forbid it. It forbids doing
it by accident.

To land a deliberate breaking change:

1. Make the change and let CI fail on it. Read the failure: `buf` names the
   message, the field number and what changed.
2. Decide it is worth it. The common case is a field rename that keeps the
   number — gRPC clients survive that, JSON clients do not, and nothing at
   runtime will tell you which you broke.
3. Note it here, under a heading for the release.
4. Cut the next baseline once the change is merged:

   ```
   git tag -a api/v0.15.0 -m "…what changed and why"   # the NEXT number
   git tag -f api/latest
   git push origin api/v0.15.0 && git push --force origin api/latest
   ```

   Then bump `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh` to it.

   The baseline releases nothing. The SDKs release themselves, as
   `sdk/go/vX.Y.Z`, from the commits that touch `sdk/` or `proto/` — see
   [releasing.md](releasing.md).

   A new number rather than `-f` on the current one: the tags are the record
   of what the contract WAS at each point, and force-moving a published tag
   redefines it under anyone who pinned it. `api/latest` is the only one that
   moves, which is what its name promises. The tag history — v0.1.0 through
   v0.14.0 — is what this procedure has actually been doing; the instruction to
   force-move said otherwise and was wrong.

The baseline is a tag rather than the default branch on purpose: a branch
moves with every merge, so comparing against `main` from `main` compares the
tree with itself and passes without checking anything.




## Unreleased — the Python SDK runs on connectrpc

- The Python SDK depends on `connectrpc` 0.12 (connect-python's successor)
  instead of `connect-python` 0.9. Messages stay `google.protobuf`: the stubs
  are generated with the plugin's `protobuf=google` option.
- An interceptor of your own reads `RequestContext` and `ResponseMetadata`
  fields as properties: `ctx.request_headers`, `ctx.method`, `ctx.timeout_ms`,
  `meta.headers` — no call.
- `ConnectError.details` are connectrpc `ErrorDetail`s rather than
  `google.protobuf.Any`. Read one with `paladin.unpack_detail(detail,
  error_details_pb2.ErrorInfo)`; attach one with `paladin.error_detail(msg)`.
- `proto_json=True` is gone from the client options: pass
  `codec=connectrpc.compat.google_protobuf_json_codec()`. `protocol`,
  `read_max_bytes` and the compression options reach every generated client
  through `transport` as before.
- `connectrpc_otel.OpenTelemetryInterceptor(client=True)` now works in
  `interceptors`, and traces each RPC.

## Unreleased — Paladin registers only the buckets it means to manage

- `CreateBucket` with `provision_on_backend` refuses a bucket the backend
  already holds (`ALREADY_EXISTS`, `BUCKET_EXISTS_ON_BACKEND`); register an
  existing bucket with `provision_on_backend` false, which in turn refuses one
  the backend does not hold (`BUCKET_NOT_ON_BACKEND`); a bucket registered
  that way is never public. The feature probe's scratch buckets cannot be
  registered (`BUCKET_RESERVED`).
- `EnsureTenantStorage` refuses a bucket that exists on the backend but is not
  registered: have an operator register it first. Registered buckets are
  unaffected.
- `Bucket.created_on_backend` says whether Paladin created the bucket. A
  delete with `delete_on_backend` is refused for any other
  (`BUCKET_NOT_CREATED_BY_PALADIN`); buckets registered before this release
  count as not created by Paladin.
- A CreateBucket on a name another S3 account owns now fails instead of
  being taken as success.

## Unreleased — background jobs pass a trashed tenant by

- Lifecycle expiry, replication, the object-trash hard-deleter, the pending
  upload reconciler and storage-event promotion, storage-layout migrations,
  batch operations and event delivery skip a tenant in the trash, and resume
  on restore. Its queued operations stay `PENDING` and its events stay
  pending meanwhile; a purge removes both with the tenant.

## Unreleased — a delete helper in both SDKs

- `paladin.Delete` (Go) and `paladin.delete` / `adelete` (Python) supply the
  `resource_version` every `DeleteObject` requires: they read the object,
  delete it at that version, read it again if it changed meanwhile (up to
  `DeleteAttempts` / `DELETE_ATTEMPTS`), and treat an object already gone as
  done. A consumer that called `DeleteObject` without a version — refused by
  the server and by both fakes — can switch to them.

## Unreleased — a tenant in the trash is frozen

- Every change to a tenant in the trash is refused — `FAILED_PRECONDITION`,
  reason `TENANT_ALREADY_DELETED` — on the data, admin and IAM planes,
  platform admins included: uploads, deletes, copies, tags, collections,
  buckets, quotas, budgets, policies, subscriptions, users, new credentials
  and `UpdateTenant`. (Any other principal was already refused by the tenant
  gate, its own tenant being the trashed one.) Reads, downloads, revocations,
  cancellations and `Get`/`List`/`Restore`/`PurgeTenant` stay open. A tenant
  holding data is removed by restoring, emptying, trashing and purging it.
- A slug a trashed tenant held and a live tenant has since taken names the
  live tenant everywhere; before, which one it resolved to was undefined.

## Unreleased — public collections

- A bucket can be created **public** (`Bucket.public_read`, with
  `provision_on_backend`) and a collection **public**
  (`Collection.access = PUBLIC_READ`): anyone may read its objects, unsigned,
  at `Object.public_url` ([ADR-0027](adr/0027-public-collections.md)). Both
  are fixed at creation, need the new Cedar action `ConfigurePublicRead`
  (built-in for `platform.admin` and `platform.tenant-provisioner`), and a
  public bucket is refused (`BACKEND_FEATURE_UNSUPPORTED`) unless the
  backend's probe found `ANONYMOUS_READ_POLICY` supported — run `TestBackend`
  first.
- In a public collection the server names objects: an upload, multipart
  initiate or copy that supplies a key is refused (`PUBLIC_COLLECTION_RULE`),
  as is a delete without `permanent=true`. `CopyObjectRequest.destination_key`
  is now optional; empty keeps the source's key, as the handler always did.
- A backend's `public_endpoint` is part of every public URL a consumer stored
  while it hosts a public bucket without `public_base_url`; changing it breaks
  them.
- New error reasons: `BACKEND_FEATURE_UNSUPPORTED` (26),
  `PUBLIC_COLLECTION_RULE` (27). Migration 049.
- Both SDK fakes serve public collections (`PublicCollection`,
  `public_collection`).

## Unreleased — a storage backend's S3 features are probed

- `TestBackend` now also probes, on a reachable backend, each S3 feature
  Paladin uses — conditional PUT, SHA-256 checksums, multipart upload,
  server-side copy, form upload, bucket creation, an anonymous-read bucket
  policy — and records what it found (migration 048). `StorageBackend` gains
  `features` and `compatibility`; the console shows both, with a warning for
  each unsupported feature.
- The probe creates and deletes a scratch bucket named `paladin-probe-…`, and
  sets and deletes a policy on it. Credentials that may not do so get those
  features reported `UNKNOWN`; the object checks then run under
  `.paladin-probe/` in the backend's configured bucket.
- `TestBackend` takes longer: up to 20 seconds more on a slow store, inside
  the admin listener's default 30-second write timeout. A deployment that
  lowered `admin.server.write_timeout` below 25 seconds should raise it.

## Unreleased — a trashed tenant's credentials stop working

- **Every plane refuses a credential whose tenant is in the trash** —
  `FAILED_PRECONDITION` with reason `TENANT_ALREADY_DELETED` — and one whose
  tenant no longer exists, `UNAUTHENTICATED`. Moving a tenant to the trash
  revokes nothing: restoring it makes its API tokens, capabilities and
  sessions work again. A change reaches every replica at once through the
  `tenant_state` notification (migration 047); a state that cannot be read
  refuses the call as `UNAVAILABLE`.
- Calls with no principal (`Login`, `RefreshToken`), and principals holding
  a platform role (`platform.admin`, `platform.capability-issuer`,
  `platform.tenant-provisioner`), are not affected: their authority is not
  their tenant's, and a trashed platform tenant must not lock out the admins
  who could restore it.

## Unreleased — an internal error no longer carries its cause

- **An RPC that fails on the server's side answers `internal error; request
  id <id>`** with its code (`internal`, `unknown`, `data_loss`), its details
  and its metadata, instead of the error's own text — which carried driver
  messages, table and constraint names. The log line and the trace keep the
  original under the same request id, returned in `X-Request-Id`; a request
  sent without one is given one. Code that parsed an internal error's message
  has nothing to parse.

## Unreleased — capability issuance answers each failure by its kind

- **`CapabilityService/Issue` and `Delegate` no longer answer every failure
  `INVALID_ARGUMENT`.** A malformed request still does. A subject tenant that
  does not exist answers `NOT_FOUND` with the new reason `TENANT_NOT_FOUND`
  — a tenant being provisioned may exist shortly, so a caller can retry on
  that reason — and one in the trash `FAILED_PRECONDITION` with
  `TENANT_ALREADY_DELETED`; before, issuing for a trashed tenant succeeded.
  A store or signer failure answers `INTERNAL`. Code that matched
  `INVALID_ARGUMENT` to mean "tenant not ready" should match the reason.
- `Delegate`'s admin path answers `NOT_FOUND` only for a parent that is not
  there; a store failure reading it is `INTERNAL`.
- The `capability` module's request errors match `ErrInvalidRequest`, and
  their messages now read `capability: invalid request: …`.

## Unreleased — a multipart download's checksum is verified

- **A multipart object completed from now on records a composite checksum**:
  the digest of its parts' digests, in S3's COMPOSITE form
  (`<base64>-<parts>`), and `ChecksumDigest.part_size_bytes` beside it. Both
  SDKs recompute it while a whole download streams and fail the read with
  `IntegrityError` on a mismatch, as they already did for an object uploaded
  in one piece.
- A multipart object completed before this release has no checksum recorded
  and is still checked for its size alone; there is nothing to backfill it
  from, since the parts' checksums were never kept.
- An older SDK ignores the new field; a new SDK against an older server sees
  no part size and does not check a composite.

## Unreleased — the SDK fakes refuse what the server refuses

- **`paladintest` (Go) validates every request** with the contract's
  protovalidate rules, as the server does: a test that uploads with no
  content type, or deletes with no `resource_version`, now fails with
  `ErrInvalidArgument` where it used to pass.
- **`DeleteObject` checks `resource_version`**, and every change to an object
  advances it: a stale version is `ErrVersionConflict`. A test that soft
  deletes and then purges must use the version after the soft delete.
- **`ListObjects` refuses `filter`, `order_by` and `sort_order`** as
  `Unimplemented`; it used to list as if they were not set.
- **Python: `paladin.testing.FakePaladin` needs the `testing` extra**
  (`paladin-sdk[testing]`), for protovalidate, and holds requests to the same
  rules — `InvalidArgumentError`, `VersionConflictError`, `UNIMPLEMENTED`.

## Unreleased — the Python SDK's `TLS` needs the `tls` extra

- **`paladin.TLS(...)` raises `ImportError` unless the SDK is installed with
  the `tls` extra**: `paladin-sdk[tls] @ git+…`. `httpcore`, `h2`, `anyio` and
  `cryptography` are no longer installed for everyone; plaintext clients and
  pyqwest's own TLS need none of them.
- **`cryptography` moved to the `tls` and `dpop` extras.** A client given
  `dpop_key=` needs `paladin-sdk[dpop]`, which it was already documented to.

## Unreleased — `CompleteMultipartUpload` returns the stored object

- **`CompleteMultipartUpload` answers with the whole object** — collection,
  key, size, checksum, timestamps — where it carried the name alone. A client
  that read the object back with `GetObject` after completing can stop; the
  SDKs' `CompleteMultipart` / `complete_multipart` still do it against a
  server that answers with the name only.
- New in both SDKs: the control half of a browser-sent multipart upload
  (`BeginMultipart`, `PresignPart`, `CompleteMultipart`, `AbortMultipart`),
  `Ensure`, and a per-key `CapabilityCache`; Python gains `capability_source`.

## Unreleased — SDK idempotency keys, errors and names

- **A context or block idempotency key no longer goes on calls the contract
  declares side-effect free or idempotent**, and the helpers give their
  repeated calls (DownloadObject on a retry, PresignPart per part) keys of
  their own; `UploadMany` / `upload_many` narrow the key to `key/<n>` per
  input. Code that relied on one key reaching every call made with it now
  sees it only on the mutating ones.
- **Python raises `PaladinError` for every failed call**: a code with no kind
  of its own (`UNAVAILABLE`, `INTERNAL`) was a bare `ConnectError`. An
  `isinstance(err, PaladinError)` used to tell the two apart is now always
  true; `InvalidArgumentError` (Go: `ErrInvalidArgument`) is new.
- **Python name constructors check their parts** as `.parse` does:
  `CollectionName("acme", "docs")` raises `InvalidNameError` (the tenant must
  be its id) instead of building a name the server refuses.
- Sessions take client options (`WithSessionClientOptions`; Python
  `transport=`, `tls=`, `**client_options`), serve a cached token while
  another is minted, and the Go SDK reports an IAM outage as `Unavailable`
  rather than `Unauthenticated`.

## Unreleased — a reused `Idempotency-Key` with a different request is refused

- **The same `Idempotency-Key` sent with a different request to the same
  method answers `InvalidArgument`** instead of replaying the first request's
  response. Before, a client reusing one key — the SDKs' context key does,
  inside their download and multipart helpers — got one object's download URL
  under every name. Use one key per request; a retry of the same request
  still replays.
||||||| parent of 4f8da27e (docs: record the SDK changes consumers can observe, and the gaps left open)


## Unreleased — `housekeeping.pending_ttl` and `delete_orphaned_parts` are removed

- **A config that sets `worker.jobs.housekeeping.pending_ttl` or
  `worker.jobs.housekeeping.delete_orphaned_parts` no longer loads.** Neither
  was read. A PENDING object expires with its presigned URL
  (`limits.presign.put_ttl`), and an abandoned multipart upload's parts are
  freed when the multipart reaper aborts it after `multipart_ttl` — whatever
  `delete_orphaned_parts` said. Delete both keys before upgrading.

## Unreleased — `storage.backends.<name>.auth.mode` has no default

- **A storage backend that names no `auth.mode` no longer loads.** The schema
  used to fill `default_chain`, so a backend configured without credentials
  quietly used whatever the AWS chain found — environment, the node's role,
  IRSA. Name the mode on every backend; the chart's `primary` already does
  (`static_keys`).
||||||| parent of 8b21df7d (fix(config)!: remove housekeeping.pending_ttl and delete_orphaned_parts)

## Unreleased — `security.reject_tenant_mismatch` is removed

- **A config that sets `security.reject_tenant_mismatch` no longer loads.**
  The key was read by nothing: the REST middleware it once switched went with
  the move to Connect, and since then a request naming another tenant has
  been refused (or, for a platform admin, acted on and audited) whatever the
  key said. The strict loader rejects it now, so delete it from any values
  file or overlay before upgrading. Nothing about tenant checks changes.

## Unreleased — a multipart upload to a taken key is `AlreadyExists`

- **`InitiateMultipartUpload` at a key another object holds answers
  `AlreadyExists`**, as `UploadObject` always has. It answered `Internal`, so
  a client could not tell a conflict it can act on from a server failure. A
  key is held by an object in any state, the trash included, until a
  permanent delete.
- **`paladintest` and `paladin.testing` hold one object per key as well**, and
  keep an upload's metadata and tags; `LookupObject` finds an object in any
  state but deleted, and `MarkFailed` / `mark_failed` fail a pending one. A
  test that uploaded twice to one key against the fake now meets
  `ErrAlreadyExists` / `AlreadyExistsError`, as it would against the server.

## Unreleased — the data plane acts on the tenant a platform admin names

- **A platform admin's data-plane calls on another tenant now reach that
  tenant.** They used to act on the admin's own tenant — empty lists, and
  writes into a same-named collection there. They are now evaluated against
  the target tenant's policies and quotas.
- **Names in one request must agree on the tenant.** A copy or batch whose
  names span two tenants is `PermissionDenied`, and so is a version name or a
  multipart completion naming a tenant other than the caller's, which used to
  be served the caller's own tenant silently.
## Unreleased — webhook deliveries no longer carry `X-Paladin-Signature`

- **The body-only `X-Paladin-Signature` is gone**, after one release
  (v10.5.0) beside `X-Paladin-Webhook-Signature`. A subscriber that still
  verifies it refuses every delivery: verify `X-Paladin-Webhook-Signature`
  with `paladin.VerifyWebhook` or `paladin.verify_webhook` instead.

## Unreleased — webhook deliveries carry a timestamped signature

- **An HTTP subscription with a signing secret now also receives
  `X-Paladin-Webhook-Signature: t=<unix seconds>,v1=<hex>`**, an HMAC-SHA256 of
  `<t>.<body>` under the same secret. Verify it with `paladin.VerifyWebhook`
  (Go) or `paladin.verify_webhook` (Python), which refuse a delivery more
  than five minutes from the subscriber's clock — a captured delivery can no
  longer be replayed later. Deduplicate on `X-Paladin-Event-Id` against a
  replay inside the window.
- **`X-Paladin-Signature` is deprecated.** It signs the body alone and so
  verifies forever once captured. It is still sent beside the new header for
  this release, and removed in the next: move verification before upgrading
  past it.

## Unreleased — `BatchUpdateTags` is capped like the other batch RPCs

- **The handler refuses more than 10 000 object ids with
  `InvalidArgument`,** as `BatchDelete`, `BatchCopy` and
  `BatchRestoreObjects` already did. The tag handler never had the check,
  and a test had pinned its absence as intentional with no reason given.
- **RPC clients see no change.** `ObjectSelector.names` is already capped at
  100 by the proto validation; the cap reaches only code that calls the
  handler directly.

## Unreleased — money is micros only; the deprecated doubles are gone

The double money fields deprecated beside `*_micros` are removed and their
numbers and names reserved: `CapabilityCaveats.max_budget_amount`,
`CapabilityServiceGetUsageResponse.spent_amount`,
`TenantBudget.max_budget_amount` and `spent_amount`,
`TenantBudgetServiceSetRequest.max_budget_amount`,
`GetTenantSummaryResponse.total_amount` and `max_budget_amount`, and `amount`
on `TopEntry` and `TimeBucket`. Read and send the `*_micros` field instead:
millionths of `unit_code`, so 25 USD is `25000000`.

- **A request that still sends a removed budget is refused** with
  `InvalidArgument`, over the binary protocol — both SDKs' default. Read as
  absent, it would have issued the capability with no budget, or lifted the
  tenant's cap to unlimited.
- **Over Connect JSON the server cannot tell.** Its JSON codec drops unknown
  names, so `"maxBudgetAmount": 25` from a JSON client is ignored and the
  budget is unlimited. Move JSON callers to `maxBudgetMicros` before
  upgrading the server.
- `max_budget_micros` stays `optional`; absent means 0, no budget.


- **`checksum_value` is required** on `UploadObjectRequest` and
  `PresignPartRequest`, and on every `CompletedPart`: base64 of the body's
  digest under the upload's `checksum_algorithm`, as S3 writes it.
  `size_hint_bytes` is now the exact size (0 is an empty object). Both are
  signed into the URL — PUT and part URLs carry `Content-Length`, the
  checksum and, for a PUT, `If-None-Match: *` — and the object store refuses
  any other body. Send every header in `required_headers`. The Go and Python
  SDKs and the console do this for you; a client that presigns itself uses
  `paladin.Checksum` / `paladin.checksum`.
- **A PUT cannot overwrite.** A second PUT through an upload URL answers 412;
  treat it as already stored and complete the object.
- **POST is a real POST policy** binding the size, Content-Type and checksum;
  submit every field in `post_policy.fields`. It cannot refuse an overwrite.
- **Promotion checks the stored bytes** against the registered size and
  checksum; bytes that differ are deleted and the object fails with
  `FailedPrecondition`.
- **`RegenerateUploadUrl` refuses objects registered before this release**
  (no size or checksum to bind): start a new upload instead.
- **Downloads may be bound to the object's ETag** with `require_etag_match`;
  the SDKs always bind theirs. A browser navigation cannot send `If-Match`,
  so the console does not.
- **MCP:** `paladin_upload_object` takes `size_bytes` (was `size_hint_bytes`)
  and a required `checksum_value`; `paladin_presign_part` and each completed
  part need `checksum_value`; `paladin_initiate_multipart_upload` now sends a
  checksum algorithm (it was refused without one).
- **Deploy order:** browser uploads send `x-amz-checksum-*`, `If-None-Match`
  and `If-Match`; the storage endpoint's CORS must allow them and expose
  `ETag` before the console is upgraded.

## Unreleased — a Biscuit copy's usage can be read

- **Go: `BiscuitCopy` names the copy's limits** (`Limits`), and names a copy
  carrying limits on a verifier without `MeterCopies` too, where it used to
  refuse it — so such a copy can be read and revoked anywhere.
- **`CopyUsageReader` reads a copy's counters**; implement it beside your
  `Meter` to show them. `CapabilityService.GetBiscuitUsage` and the console's
  "Copy usage" serve them.

## Unreleased — a Biscuit copy can carry request and budget limits of its own

- **Go: a `Meter` of your own should count `Copies`.** `RequestBump`,
  `ChargeRequest` and `ReserveRequest` carry the presented copy's limits;
  check and count each under its revocation id, record the ids on the charge
  and the reservation, and return to them on refund, settle and release.
  `memstore` shows how. Then set `VerifierConfig.MeterCopies`; until you do, a
  copy carrying limits is refused with `ErrCopyCountersNotMetered`.
- **Pass `cap.Copies` on** wherever you build those requests from a verified
  capability.
- **Python: `attenuate` takes `max_requests` and `max_budget_micros`.**
- **`Delegate` from a Biscuit copy narrows from the copy.** A child used to
  be narrowed from the capability's stored record, so it could regain what the
  copy had given up offline. A copy with limits of its own cannot delegate at
  all (`FailedPrecondition`): attenuate it instead.

## Unreleased — one copy of a Biscuit can be revoked on its own

- **Go: a verifier with `AcceptBiscuit` needs `BiscuitRevocations`.**
  `NewStandardVerifier` refuses the config without it. Implement
  `BiscuitRevocationStore` beside your `Store` (`memstore.Store` does) and
  pass it wrapped in `NewCachedBiscuitRevocationChecker`, cleared with the
  other cache when a revocation is announced.
- **Revoking a copy reaches only what was attenuated from it.** The copy it
  came from, its siblings and the capability's JWT keep working; revoke the
  capability to stop them all.

## capability/v0.10.0 — the DPoP replay cache is shared across replicas

- **Go: `ReplayCache.Seen` and `DPoPVerifier.Check` take a
  `context.Context` first.** Pass the request's context to `Check`; a
  `ReplayCache` of your own adds the parameter to `Seen` and should honour it.
  `MemoryReplayCache` ignores it.
- **A `ReplayCache` that cannot answer should report the id as seen.** The
  proof is then refused rather than accepted unrecorded.
- **Paladin keeps proof ids in Postgres** (`dpop_seen_jti`, migration 037)
  instead of per process, so a proof replayed against another replica is
  refused. Every request with a key-bound capability costs one write; the
  capability purger deletes expired ids on its existing interval.

## Unreleased — batches act only on their own collection, and take restricted capabilities

- **A batch acts only on objects of the collection it names.** An object id
  from another collection of the tenant is reported as not found, like an id
  that does not exist; before, the worker acted on it although the batch was
  authorised for its own collection only.
- **A resource-restricted capability can run `BatchDelete`,
  `BatchUpdateTags`, `BatchRestoreObjects` and `BatchCopy`.** Each object the
  batch acts on must be in scope, or the batch is refused with
  `PermissionDenied` — as is one that would act on none of them, since
  nothing in it is shown to be in scope.
- **`BatchCopy` needs `get` as well as `put`** from a capability — `get` on each
  source, `put` on each destination — and a capability without
  `AllowTaintedRead` cannot copy a tainted object.
- Calls made with a JWT or API token and no capability are unaffected.

## Unreleased — capabilities need the op for operations and storage bootstrap

- **`OperationService` asserts a capability op.** `GetOperation` needs `get`,
  `ListOperations` `list`, and `CancelOperation` `manage`. A capability
  restricted by resource prefix or URI is refused all three, as it already was
  for the batch RPCs that create operations.
- **`EnsureTenantStorage` needs `manage`.** A capability alone authenticates a
  data-plane call and the Cedar permit is tenant equality, so until now any
  capability of the tenant could provision its buckets and collections.
- Calls made with a JWT or API token and no capability are unaffected.


## Unreleased — uploads are held to `limits.*` and bucket constraints

- **`limits.*` is enforced.** `max_object_size` now refuses a larger single
  PUT/POST, `max_multipart_size` a larger multipart upload, and
  `allowed_content_types` any other media type, all with `InvalidArgument`.
  These were documented as enforced and were not; a deployment that relied on
  their being ignored — the chart ships an allowlist — must widen them first.
  A bucket's constraints (`max_object_size_bytes`, part limits,
  `allowed_content_types`, `required_checksum_algorithm`, presign TTL
  ceilings) now apply to uploads, copies and URLs for that bucket, narrowing
  the global limits.
- **Config keys removed.** `limits.presign.default_max_size` (use
  `limits.max_object_size`) and, since #215, `limits.presign.default_ttl` (the
  per-method `put_ttl`/`get_ttl`/`part_ttl` are the defaults). The loader
  rejects unknown keys, so delete them from any override.
- **Part sizes are IEC.** `min_part_size`/`max_part_size` default to `5MiB`/
  `5GiB`; a `min_part_size` below S3's 5 MiB, including the old `5MB`, fails
  at load.
- **Presign TTLs are refused, not shortened.** A TTL above `max_ttl` (or a
  bucket ceiling) is `InvalidArgument` on every presigning RPC, and
  `max_ttl` may not exceed 168h.
- **Bucket creation validates constraints.** Constraints no upload could
  satisfy are refused by CreateBucket.

## Unreleased — the Python SDK knows its version under Poetry

- **`sdk_version()`, the `User-Agent` and errors report the release** when
  the package is installed from its git tag by a tool that builds without
  git, as Poetry does through Dulwich. Such a build still records
  `0.0.0+unknown` in its metadata; the SDK now reads the tag the installer
  recorded in `direct_url.json` instead. Nothing to change on the caller's
  side.

## v0.23.0 — TLS that a consumer can wrap, verify and close

- **Go: `TLS.RoundTripper()` returns the rotating transport** `WithTLS` and
  `WithTransferTLS` use, as a `*RotatingTransport`, for a client of your own
  that wraps it — give it to `WithHTTPClient` or `WithTransferHTTPClient`.
  After a rotation every new request, HTTP/2 included, goes on a connection
  with the new files; before, a busy HTTP/2 connection kept taking requests
  on the old certificate. `TLS.Transport()` is unchanged.
- **Python: `TLS(server_id=…, verify_peer=…, min_version=…)`**, as in Go,
  with the same errors by name (`ServerIDError` and the rest, each a
  `ValueError`). The connections under `TLS` now run on the standard
  library's `ssl` under httpcore, which the SDK depends on: pyqwest cannot
  check a peer. After a rotation no request goes on an old connection, and the
  old ones close once idle. A pyqwest-only setting passed to
  `TLS.sync_transport()` still works, with a `DeprecationWarning`, for one
  release.
- **Python: the exit abort reported against 0.17 did not reproduce.** 200
  runs each on macOS — 0.17.0, 0.17.0 with grpcio, and this release over
  mutual TLS — exited cleanly, so nothing changed for it. A stress test now
  makes and drops clients and transfers in fresh interpreters, 200 times per
  CI run, and fails on any abort.

## Unreleased — the SDKs answer a consumer's review

The contract does not change. What a caller may notice:

- **Python: a generated client takes `http_client=client.http_client()`.**
  The SDK reads the server's release and `Retry-After` at the transport now,
  not from connect-python's internals. `connect()` builds such a client
  itself; one built without it raises errors with no `server_version` and
  warns once. An `http_client` of your own wraps its transport in
  `paladin.RelaySyncTransport`.
- **Go: an RPC through `WithTLS` has no response-header timeout**, as without
  TLS; its context bounds it. Transfers keep theirs.
- **Go: `TLS.MinVersion`** raises the floor from TLS 1.2. After a rotation,
  connections on the old files close once idle.
- **The fakes complete without an ETag**, return a completed object on a
  repeat, and serve `EnsureTenantStorage`, as the server does.
- **A Python build without git** reads its version from a tag's source
  archive, or reports `0.0.0+unknown` instead of `0.0.0`.


## v0.14.0 — the Python SDK's transfers are declared once, stream and verify

The contract does not change. The Python SDK's `upload` and `download` now
match the Go SDK's, and break code written against 0.13:

- **Presigned requests go through a `Transfer`.** Pass one to
  `connect(…, transfer=paladin.Transfer(…))`; without it a shared default is
  used. They no longer go through `urllib`, so a proxy or TLS setting made for
  `urllib` no longer reaches them: give the `Transfer` a `transport`.
- **Redirects are refused.** A redirect from storage is now a
  `TransferError` with its 3xx status.
- **`TransferError(method, host, status, body)`** gains `host`, the host the
  request went to.
- **A whole download is verified**: `IntegrityError` when the size or the
  recorded checksum does not match. `download_stream` streams the content
  instead of returning it whole; `download` still returns bytes.
- **A single-PUT upload records its SHA-256** on the object.

## v0.13.0 — the Go SDK's transfers are declared once, stream and verify

The contract does not change. The Go SDK's `Upload` and `Download` do, and
break code written against 0.12:

- **The HTTP client for presigned requests moves to the client.**
  `UploadOptions.HTTPClient` and `Download`'s `httpClient` argument are gone.
  Declare it once with `paladin.WithTransfer(paladin.NewTransfer(
  paladin.WithTransferHTTPClient(c)))`; every `Upload` and `Download` through
  that client uses it.
- **`Download` returns an `*ObjectReader`.** `Download(ctx, data, name, nil)`
  becomes `Download(ctx, data, name, paladin.DownloadOptions{})`; the reader is
  the `io.ReadCloser` it returned, and `r.Object` the object.
- **A whole download is verified.** Its last `Read` returns an
  `*IntegrityError` rather than `io.EOF` when the size or the recorded
  checksum does not match. Code that treated any error at the end of the
  content as the end of the content now sees it.
- **Redirects are refused.** The default transport used to be
  `http.DefaultClient`, which follows them; a presigned URL never redirects
  legitimately, and following one sends its signed headers elsewhere. A
  redirect is now a `*TransferError` with its 3xx status.
- **A single-PUT upload records its SHA-256** on the object, through
  `CompleteObject`'s `checksum_value`.

## v0.12.0 — the SDKs sign in, connect every plane, and stamp idempotency keys

The contract gains one RPC, `EventSubscriptionService.RedriveFailedDeliveries`;
nothing in it changes. The SDKs change, and three of the changes break code
written against 0.11 (ADR-0018):

- **Every call with side effects carries an `Idempotency-Key`.** A unary call
  whose `idempotency_level` is unknown gets the context's key, else its
  request's `idempotency_key` field, else a fresh one, kept across retries.
  Each call without a key of its own gets a fresh one, so two calls are still
  two operations; only a key held across several calls — one context from
  `WithIdempotencyKey`, one `idempotency_key` block around a loop — answers
  every call after the first with the first response. Set a fresh key per
  logical operation. A call that must go out with no key at all takes
  `WithoutIdempotencyKey` / `no_idempotency_key`.
- **Such calls are now retried** on `Unavailable` and `ResourceExhausted`
  under `WithRetries`/`Retry`, since they carry a key; before, only calls
  made with an explicit key were.
- **A retry that cannot start before the deadline is not made**, and the
  server's error is returned rather than the context's. Code that matched
  `context.DeadlineExceeded` after a retried call now sees the server's code.

Added: `Session`/`AsyncSession` and `StaticToken`, `Connect`/`connect` with a
client for every service of each plane, `Pages`, `Wait`, `Mask`, `Upload`,
`Download`; `WithAPIToken`/`api_token`, `WithHeader`/`headers`, and a
`User-Agent` naming the SDK. The Python `Client` takes `token_source` and
`audience`. Retries wait a random share of the doubling ceiling and never less
than the server's `Retry-After`.

## Unreleased — clients read the contract; the three prefix lists are gone

No proto change and no wire change. `backend/internal/rpcmeta` answers "is this
a read" and "should a client stamp an Idempotency-Key" from the descriptor, and
the console, the seeder, the MCP bridge and the server interceptor all ask it.

**What a client should send, stated once.** Stamp a key iff the method declares
`IDEMPOTENCY_UNKNOWN`. `NO_SIDE_EFFECTS` changes nothing, so a key buys a row
in `idempotency_keys` and a chance of serving a stale snapshot. `IDEMPOTENT` is
already safe to repeat, so a key buys a row and no safety. What is left is
every call whose repeat nobody has promised anything about.

**Two behaviours change**, and both are the descriptor being right where the
prefixes were not:

- `RestoreObjectVersion` no longer gets a key. The `Restore` prefix swept it
  in; it takes `resource_version`, so a repeat writes the same value or fails
  `Aborted`.
- Credential minting — `Login`, `RefreshToken`, `ExchangeAudience`,
  `SwitchTenant` — now does. Clients used to withhold it because replaying a
  rotated refresh token is wrong. That is true, and it is enforced on the
  server, which refuses to memoize those four at all
  (`middleware.CredentialMintingProcedures`). An Idempotency-Key is a caller's
  de-duplication token, not a request to cache. Methods that mint a credential
  and are memoized (`CapabilityService/Issue`, `APITokenService/Create`,
  `UserService/ResetPassword`) store their response with the credential cleared
  and answer a repeated key with `AlreadyExists`.

If you integrate with Paladin, this is the rule to implement. `buf` puts
`idempotency_level` in every generated descriptor;
`@bufbuild/protobuf` exposes it as `method.idempotency`, and the Go runtime as
`MethodOptions.GetIdempotencyLevel()`.

## Unreleased — 112 of 142 RPCs declare an idempotency_level

Nine more: one read and eight idempotent writes. The remaining 30 are the
honest residue — every one of them creates, mints, charges or submits, and
`IDEMPOTENCY_UNKNOWN` is the right answer for all of them.

**`GetDispatcherStats` was never a write.** It was reported as one because the
verb list matched prefixes, and `Dispatch` swallowed `DispatcherStats` — an
outbound HTTP GET for the dispatcher's stats page. The verb now has to end at a
word boundary. A genuine read stayed undeclared for months for looking guilty.

**Two are OCC-guarded**: `RestoreObjectVersion` and `RenameTenantSlug` carry
`resource_version`, so a repeat writes the same value or fails `Aborted`.

**Six carry a written reason**, each read out of the SQL rather than inferred
from the verb — which is what made `RotateCredentials` look safe:
`SetObjectLegalHold` and `SetObjectRetention` are `ON CONFLICT DO UPDATE` to
the value supplied; `GrantScopes` merges through a dedup keyed on the scope
string, so it is a set union; `ResetPassword` sets the password to the one
given (the bcrypt hash differs per call because the salt does, but which
password works does not); `UpdateMine` upserts one settings row; and
`TestBackend` re-probes and overwrites the health row — the same RPC whose
proto comment says "Read-only" while it writes, which is why it is idempotent
and not a read.

**`RestoreTenant` and `ChangePassword` are deliberately left undeclared.** Both
converge on state, and both answer a repeat with an error: the restore query is
`WHERE deleted_at IS NOT NULL`, so a second call touches no row and returns
`ErrNotTrashed`, and `ChangePassword` checks the old password, which the first
call already invalidated. A proxy retrying either would report a failure for an
operation that succeeded. The OCC pair above answer `Aborted`, which is the
standard "re-read and retry" signal; these two do not.

**Cutting the baseline.** This needs `api/v0.9.0`.

## Unreleased — 103 of 142 RPCs declare an idempotency_level

Thirteen more `NO_SIDE_EFFECTS`, and they are the ones the earlier passes said
were unreachable: `GetHealth`, `GetQuota`, `GetObjectTags`, `GetObjectLock`,
`GetObjectVersion`, `ListObjectVersions`, `GetSubscription`,
`ListSubscriptions`, `GetTenantDefaultBinding`, `GetConfig`,
`GetPlatformStats`, `Summarize`, and `HealthService.GetVersion`.

**Why they were invisible, in three layers.** The guard globbed `handler*.go`,
a prefix match that never opened `lock_handler.go` or `version_handler.go`. It
accepted only `*Handler` receivers, while the Connect layer's are `*…Server`.
And it indexed per package, encoding an assumption the tree does not honour:
`QuotaService.GetQuota` is a method on `*QuotaServer` that dispatches into
`H.GetTenantQuota` — a different name, in a different package. Each of the
three hid the others. Handler bodies actually checked went from 52 to 108.

**The write detector was answering wrongly too.** A probe over what was still
undeclared reported `RenameTenantSlug`, `Delegate`, `Issue` and
`MigrateTenantStorageLayout` as writing nothing. They call `Rename`,
`Delegate`, `Mint` and `Migrate` — words the verb list did not have. Three
rounds of adding words is the honest measure of a name-based detector: it finds
what someone thought to name, which is why every annotation is still read by
hand and this only keeps it true afterwards.

**`DownloadObject` is deliberately NOT annotated.** With the deeper walk it
reports `RecordPresign` and `ChargeRequest`: it records the presign it issues
and charges the tenant's quota. It reads like a read and is not one.

**39 remain undeclared, and none of them look like a read** — the census test
prints the list on every run, so the remaining gap is visible rather than
inferred.

**Cutting the baseline.** This needs `api/v0.8.0`.

## Unreleased — 90 of 142 RPCs declare an idempotency_level

Extends the previous entry; the same procedure and the same reasoning. 54 more
declarations: 44 `IDEMPOTENT` and 10 `NO_SIDE_EFFECTS` on method names that
exist on two services each.

**What an intermediary may now do.** `IDEMPOTENT` invites a retry on failure.
Every RPC carrying it is one of three shapes, and a test enforces that: it
takes `resource_version` (a repeat either writes the same value or loses the
OCC check and fails Aborted), it removes something (removing what is gone is a
no-op), or it appears in `idempotentByArgument` with a written reason. An
earlier draft declared `RotateCredentials` idempotent — two rotations mint two
credential pairs — and that guard now fails on exactly that.

**Still unannotated: 52.** Most genuinely need an Idempotency-Key and
`IDEMPOTENCY_UNKNOWN` is the honest value. Fourteen do not: they are reads
whose handlers hang off receivers the guard's index did not reach
(`*VersionHandler` and similar). They stay unannotated until each is found and
checked, because an annotation nothing verifies is the failure this work is
about. `RestoreObjectVersion` is also held back deliberately — it makes a named
version current and demotes the previous one, and what a repeat does was not
clear enough from the code to promise anything.

**Cutting the baseline.** This needs `api/v0.7.0`.

## Unreleased — 36 read RPCs declare `idempotency_level = NO_SIDE_EFFECTS`

A deliberate breaking change under the procedure above: `buf` puts
`RPC_SAME_IDEMPOTENCY_LEVEL` in the WIRE category, so declaring the option at
all trips the gate. Nothing about the request or response bytes changes.

**What it means for a client.** `NO_SIDE_EFFECTS` tells gRPC intermediaries the
call is safe to retry on their own. For these 36 it is: each was checked
against its handler, not against its name. If you run a proxy that acts on the
option, expect it to start retrying these reads — which is the point.

**Why only 36 of 142.** The remainder are unannotated, and
`IDEMPOTENCY_UNKNOWN` — the default — is the honest value for them: it promises
nothing. Annotating the rest needs the same per-handler check, and a wrong
annotation is a wrong wire contract that costs another baseline to fix. Three
draft classifications built from method-name prefixes each got RPCs wrong
(`RotateCredentials` is not idempotent; `BatchDeleteObjects` is), and the proto
comment on `TestBackend` says "Read-only" while the handler persists the probe
result via `SetHealth`. Names and comments were not a usable source; handlers
were.

**What keeps it true.** `internal/api/idempotency_contract_test.go` parses
every handler behind a NO_SIDE_EFFECTS RPC on each build and fails if it
reaches a write, directly or one hop through a helper. Verified by mutation in
both shapes.

**Cutting the baseline.** This needs `api/v0.6.0`; see the procedure above.
Until it is tagged and `breaking_against` bumped, `verify-all` fails on
this change by design.

## Unreleased — API tokens are addressed by resource name

`APITokenService` was the only service in the API where `name` did not mean a
resource name, and the only one whose RPCs took a bare uuid.

| Message | Was | Now |
| --- | --- | --- |
| `APIToken.name` | the operator's label, e.g. `ci-uploader` | the resource name, `tenants/{tenant}/apiTokens/{id}` |
| `APIToken.display_name` | — | the label (field 3, formerly `name`) |
| `Create.tenant_id` | bare uuid | `parent`, `tenants/{tenant}` |
| `Create.name` | the label | `display_name` |
| `List.tenant_id` | bare uuid | `parent`, `tenants/{tenant}` |
| `Revoke.id` | bare uuid | `name`, `tenants/{tenant}/apiTokens/{id}` |
| `GetUsage.id` | bare uuid | `name`, same shape |
| `GetUsageResponse.id` | bare uuid | `name`, echoes the request |

**Why it had to change rather than being tidied later.** `api_tokens` carries
FORCE row-level security keyed on the session tenant. An RPC holding only an id
cannot scope its query, and cannot discover the tenant either — the row that
would tell it is the one RLS is hiding. So `Revoke` and `GetUsage` were
unreachable across tenants by construction: a platform admin could see another
tenant's tokens and not revoke one, which is the operation the visibility is
for. Carrying the parent in the address breaks that circle without adding a
privileged read path around the enforcement mechanism.

**What callers have to change**

- `Create({tenant_id, name})` → `Create({parent: "tenants/<t>", display_name})`.
- `List({tenant_id})` → `List({parent: "tenants/<t>"})`.
- `Revoke({id})` → `Revoke({name: "tenants/<t>/apiTokens/<id>"})`.
- `GetUsage({id})` → same `name` shape; the response field is `name` too.
- Anything reading `APIToken.name` as a label reads `display_name` now. Note
  this is the dangerous one for a JSON client: the field still exists and still
  carries a string, so nothing fails — it just renders a resource name where a
  label used to be.

`APIToken.id` and `tenant_id` are unchanged and still returned, so a caller
that wants the raw parts does not have to parse the name.

## Unreleased — a failed operation fills `Operation.error`, not `Operation.response`

`Operation.result` is a oneof of `google.rpc.Status error` and
`google.protobuf.Any response`. Both API planes used to put every stored
payload — failures included — into `response`, and never set `error` at all.
The stored `error_code` and `error_message` reached no client, and a caller
that asked `result.case == "error"` to tell a failure from a success got
"success" for every failed operation. The console's ops drawer and dashboard
widget both asked exactly that.

Now an operation in FAILED or CANCELLED fills `error`:

- `code` — the canonical code: `CANCELLED` for a cancellation, `ABORTED` for
  `WORKER_LOST` (the work may be half-applied), `UNIMPLEMENTED` for an
  unknown operation type, `UNKNOWN` otherwise.
- `message` — the stored `error_message`, falling back to the code when the
  row has no message.
- `details[0]` — the payload that used to be in `response`, unchanged, as an
  `Any`-wrapped `Struct`. A reclaimed operation's payload also carries
  `last_progress: {processed, total}`: the last snapshot the worker wrote
  before it died, which is a lower bound — progress writes are throttled to
  about one a second.

A client that read failure detail out of `response` needs to read
`error.details[0]` instead. A client that only reads successes is unaffected:
the `response` arm still carries them.

## Unreleased — `force` is gone from the delete RPCs

One word meant four different things, and two of them were dangerous.

| RPC | `force` used to mean | Now |
| --- | --- | --- |
| `DeleteTenant` | skip the trash and hard-delete | **removed** — `DeleteTenant` trashes, `PurgeTenant` destroys |
| `DeleteBackend` | "delete even if buckets reference it" | **removed** — it never could; `buckets.backend_id` is `ON DELETE RESTRICT` |
| `DeleteBucket` | waive the OCC guard | renamed `skip_version_check` |
| `DeleteCollection` | documented as "ignore objects", implemented as "waive OCC" | renamed `skip_version_check`, doc corrected |

An integrator who learned the word on tenants and applied it to buckets was
turning off the concurrency check while believing they were insisting harder.
That is the kind of mistake an API should make impossible to make.

`skip_version_check` now means exactly one thing everywhere it appears: waive
the optimistic-concurrency guard. Nothing else in the API is spelled `force`.

**What callers have to change**

- `DeleteTenant(force=true)` → `DeleteTenant(...)` then `PurgeTenant(...)`.
  Two calls, and the destructive one names itself.
- `DeleteBackend(force=true)` → delete the buckets first. The flag never
  worked; it only turned a clear conflict into a raw SQLSTATE under
  `Internal`.
- `DeleteBucket(force=…)` / `DeleteCollection(force=…)` → rename the field to
  `skip_version_check`. Same meaning, same behaviour.
- `resource_version` is now required by the schema on `DeleteTenant` and
  `DeleteBackend`, not by a handler branch — with no bypass flag left, there
  is nothing for an omission to be legitimate for.

Field numbers 3 on `DeleteTenantRequest` and `DeleteBackendRequest` are
reserved, so nothing can silently reuse them.
