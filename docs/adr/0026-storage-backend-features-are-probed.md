# ADR-0026: A storage backend's S3 features are probed, recorded and shown

- **Status:** Accepted — implemented 2026-10-07 (`048_storage_backend_features.sql`,
  `internal/storage/features`, `s3adapter.ProbeFeatures`, `TestBackend`).
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (backends and
  buckets), [ADR-0027](0027-public-collections.md) (the first operation gated
  on a feature), the S3 conformance suite
  ([`backend/tests/conformance`](../../backend/tests/conformance/README.md)).

- **Context.** Paladin talks to "S3" and means a specific subset of it: a PUT
  bound to `If-None-Match: *` so an upload cannot replace an object, a
  `x-amz-checksum-sha256` the store verifies, multipart uploads, server-side
  copy, browser form uploads, bucket creation and, with public collections,
  an anonymous-read bucket policy. Every self-hosted store implements a
  different subset, and the gaps do not fail loudly: a store that ignores
  `If-None-Match` lets an upload replace bytes with a 200; one that ignores a
  checksum header stores corrupt bytes with a 200; one that accepts a bucket
  policy and never evaluates it answers 403 to everyone. `StorageBackend.kind`
  and `provider` name the store but say nothing about which of these hold, and
  a version of the same store can differ from the last.

- **Decision.**
  1. **A catalog of the features Paladin uses**, in Go
     (`internal/storage/features`), each marked *required* — a guarantee
     Paladin makes depends on it — or *optional* — one operation needs it.
     The proto enum `StorageFeature` mirrors it; a test holds the two equal.

     | Feature | Kind | What depends on it |
     |---|---|---|
     | `CONDITIONAL_PUT` | required | an upload never replaces an existing object |
     | `CHECKSUM_SHA256` | required | the store refuses bytes that do not match the declared checksum |
     | `MULTIPART_UPLOAD` | required | uploads above the multipart threshold |
     | `SERVER_SIDE_COPY` | optional | `CopyObject` |
     | `PRESIGNED_POST` | optional | browser form uploads (`transport_post`) |
     | `BUCKET_CREATE` | optional | `provision_on_backend`, the dedicated layout |
     | `ANONYMOUS_READ_POLICY` | optional | public collections |

  2. **Probed, not declared.** `TestBackend` exercises each feature against
     the store and observes the outcome: it writes a second object with
     `If-None-Match: *` and expects 412, uploads bytes under a wrong checksum
     and expects a refusal, sets a policy granting anonymous read on one
     prefix and expects an unsigned GET to succeed there and fail beside it.
     A feature is `SUPPORTED`, `UNSUPPORTED` (the store answered and the
     answer is wrong), or `UNKNOWN` (the probe could not tell: no scratch
     bucket, a timeout, a denied permission). A declaration in config would be
     the operator's belief about the store; the probe is the store's answer.
  3. **Where the probe writes.** A scratch bucket `paladin-probe-<random>`,
     removed afterwards, so the probe never touches a bucket's policy or
     events. When the store refuses to create one, the object probes run under
     `.paladin-probe/<run>/` in the backend's configured bucket — a prefix no
     tenant key can take, since every tenant key starts with a UUID — and the
     bucket-level probes are `UNKNOWN`.
  4. **Recorded per backend** in `storage_backend_features`, one row per
     feature, replaced on each probe. Outside `resource_version`, like
     `storage_backend_health`: a probe is an observation, not configuration.
  5. **Shown.** `StorageBackend.features` lists every catalog feature —
     `UNKNOWN` for one never probed — and `compatibility` summarises them:
     `INCOMPATIBLE` when a required feature is `UNSUPPORTED`, `COMPATIBLE`
     when every required feature is `SUPPORTED`, `UNVERIFIED` otherwise. The
     console shows the summary on each backend and names every unsupported
     feature with what it disables.
  6. **Gates use it for optional features only.** An operation that needs an
     optional feature refuses on a backend where that feature is not
     `SUPPORTED`, with `FAILED_PRECONDITION` naming the feature. Required
     features are not gated per call: an incompatible backend is a deployment
     error the console shows, not a condition every upload should re-check.

- **Consequences.**
  - Until someone runs `TestBackend`, every feature is `UNKNOWN` and any
    gated operation refuses. That is deliberate: an unprobed store has not
    shown it can do the thing.
  - The probe needs permission to create and delete a bucket, set and delete
    a bucket policy, and write and delete objects. Credentials scoped tighter
    get `UNKNOWN` for what they cannot try, which the console says.
  - A probe result goes stale when the store is upgraded or reconfigured. It
    carries `checked_at`; re-probing is the operator's step, and periodic
    re-probing is in BACKLOG.
  - With S3 event notifications on, the probe's writes reach the ingest
    pipeline, which drops them: their key does not start with a tenant UUID.

- **The probe and the conformance suite.** The suite is run by a developer,
  against an endpoint of their choosing, and measures everything it can: it
  is how a new store is evaluated. The probe runs in the deployment, against
  the backend's own credentials, and checks the few features operations are
  gated on or that Paladin's guarantees need. A feature the probe adds is
  worth adding to the suite's profile too.

- **Alternatives considered.**
  - *A static matrix per `provider`.* Wrong for any version or configuration
    the table did not anticipate, and silent about it — the failure this ADR
    exists to remove.
  - *Discover failures in production and map the errors.* Several of these
    gaps do not produce an error at all.
  - *Gate every operation on every feature.* A required feature missing is a
    reason not to run on that store at all; checking it per request adds
    latency to every call and changes nothing an operator should not have
    seen in the console first.
