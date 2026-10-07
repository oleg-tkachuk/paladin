# ADR-0027: Public collections — anonymous reads from a public bucket

- **Status:** Accepted — implemented 2026-10-07.
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (buckets and
  their binding), [ADR-0026](0026-storage-backend-features-are-probed.md)
  (the `ANONYMOUS_READ_POLICY` feature this depends on),
  [deletion-semantics](../deletion-semantics.md).

- **Context.** Every read is a presigned download: one Paladin call per read,
  and a URL that expires. That is right for private data and wrong for content
  published on purpose — a picture shown on a public page to whoever opens
  it. A consumer of such content wants to store a URL once and have browsers
  and a CDN fetch it with no call to anyone per view.

  Four facts of the existing system shape the design:
  - The physical key is `<tenant>/<collection>/<key>`, and `key` is the
    client's choice or, when omitted, a UUIDv7 — 74 random bits.
  - Deleting an object without `permanent` moves it to the trash and leaves
    its bytes in storage; the hard-delete TTL is off by default. The
    lifecycle worker only ever soft-deletes.
  - An S3 bucket carries one policy document. Granting a prefix per
    collection means rewriting that document on every collection created
    (racing writers, a size limit — 20 KB on AWS) and one wrong wildcard
    opens every tenant sharing the bucket. Some stores have no bucket
    policies at all.
  - Not every S3 store enforces an anonymous-read policy, or enforces only
    the prefix it names; ADR-0026 probes exactly that.

- **Decision.**
  1. **Public is a property of a bucket and is declared by each collection.**
     `Bucket.public_read` is set when the bucket is created and never
     changes. `Collection.access` is `PRIVATE` or `PUBLIC_READ`, set when the
     collection is created and never changes. A database trigger holds a
     collection's access equal to its bucket's: a public collection binds
     only to a public bucket, a private one only to a private bucket, and a
     public collection never rebinds, since its objects' URLs name the
     bucket. The isolation is the bucket boundary, which the store enforces;
     nothing depends on getting a prefix right.
  2. **Paladin owns the policy.** A public bucket must be created with
     `provision_on_backend`. The bucket reconciler creates it and sets one
     static statement granting anonymous `s3:GetObject` on the whole bucket,
     rendered from `policies/anonymous_read.json.tmpl`; the bucket is
     `ready` only once the policy is set. Listing, writing and deleting stay
     signed.
  3. **Only where it works.** Creating a public bucket is refused with
     `FAILED_PRECONDITION` / `BACKEND_FEATURE_UNSUPPORTED` unless the
     backend's last probe found `ANONYMOUS_READ_POLICY` supported, and on a
     backend encrypting with SSE-KMS, whose objects an anonymous GET cannot
     decrypt.
  4. **A security decision with its own permission.** Creating a public
     bucket or a public collection needs the Cedar action
     `ConfigurePublicRead` besides the usual one. Its only built-in grants
     are `platform.admin` and `platform.tenant-provisioner`; no tenant role
     under the default policy, data-plane credential or capability holds it.
     The self-service paths (`EnsureTenantStorage`) never create a public
     bucket or collection.
  5. **Keys a stranger cannot guess.** In a public collection the server
     names every object: 128 bits from `crypto/rand`, base32. A key supplied
     by the client — on upload, multipart initiate or copy into the
     collection — is refused, and a batch copy, which names its copies after
     their sources, cannot target one.
  6. **Only passive content.** A public bucket must list its allowed content
     types, and none of them may be one a browser executes or renders as a
     document (`text/html`, `image/svg+xml`, JavaScript, XML and their
     relatives). Served from the store's host to anyone, those are stored
     XSS. Every upload into a public collection is checked against the same
     list, whatever the bucket allows.
  7. **Cacheable.** Every object in a public collection is stored with
     `Cache-Control` — the collection's `cache_control`, by default
     `public, max-age=31536000, immutable` — bound into the signed PUT, the
     form-upload policy and the multipart initiate. Content changes by
     writing a new object, which gets a new key.
  8. **A URL to store.** `Object.public_url` carries the object's address:
     the bucket's `public_base_url` (a CDN in front of it) when set, else the
     backend's `public_endpoint`, followed by the physical key. The adapter
     builds it with the SDK's endpoint resolver, as a presigned URL is built.
     `public_base_url` is set at creation and never changes, and so, while a
     public bucket exists, is its backend's `public_endpoint` in practice:
     every URL a consumer stored depends on it.
  9. **Delete means gone, at the store.** A public collection has no trash:
     `DeleteObject` without `permanent` is refused (a batch delete reports
     each such object as failed), and a public bucket takes no lifecycle
     rules (the worker only soft-deletes). A permanent
     delete removes the bytes, and the URL answers 404 from then on. A CDN
     may serve its copy until its TTL — that is the CDN's contract with the
     consumer, not Paladin's.
  10. **Unchanged:** quotas, audit and events for writes, as in any
      collection. Anonymous reads reach no Paladin process, so they are not
      attributed to a tenant or counted; the store's or the CDN's access logs
      are the only record. Moving a tenant to the trash does not touch its
      bytes, so its public objects stay readable until they are deleted —
      `PurgeTenant` refuses while the tenant has objects.

- **Consequences.**
  - Public objects never share a bucket with private ones, so a deployment
    publishing anything runs at least one more bucket per backend. Tenants
    may share a public bucket: what isolates them there is the unguessable
    key, which is the whole access model of a public object anyway.
  - An object is readable at its URL as soon as its bytes reach the store,
    before `CompleteObject` promotes it: the store knows nothing of Paladin's
    states. Its key is unguessable and returned only to its uploader, so
    nobody else can read it early.
  - A collection cannot be made public, or private, after the fact.
    Publishing private content is a copy into a public collection; taking
    content down is deleting it.
  - The fakes in both SDKs serve a public collection's objects unsigned at
    their `public_url`, so consumers test the path they ship.

- **Alternatives considered.**
  - *A policy statement per public collection, in the shared bucket.* The
    one-document-per-bucket problem above, and a scoping bug would expose
    every tenant on the bucket. The probe shows SeaweedFS can do it; the
    design should not depend on it.
  - *A Paladin endpoint that redirects to a presigned URL.* Every view would
    reach Paladin, and a redirect is not cacheable for longer than its
    signature lives — the two things this feature exists to remove.
  - *Making visibility changeable.* Turning a collection private again would
    need the store to stop serving URLs already handed out and cached; with
    the bucket as the boundary that is a copy and a delete, and a flag would
    only pretend otherwise.
