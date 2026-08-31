# S3 conformance suite

What a storage backend has to do for Paladin to work, asserted against a real
endpoint — and what it merely *may* do, recorded rather than demanded.

Run it against any S3-compatible endpoint:

```bash
PALADIN_CONFORMANCE_ENDPOINT=http://localhost:9000 \
PALADIN_CONFORMANCE_ACCESS_KEY=… \
PALADIN_CONFORMANCE_SECRET_KEY=… \
PALADIN_CONFORMANCE_PROVIDER=minio \
  go test -tags=conformance ./tests/conformance/ -v
```

Optional: `PALADIN_CONFORMANCE_REGION` (default `us-east-1`),
`PALADIN_CONFORMANCE_BUCKET` (default: a fresh `paladin-conf-<hex>` the suite
creates and removes), `PALADIN_CONFORMANCE_PATH_STYLE`,
`PALADIN_CONFORMANCE_AUTH_MODE`.

## Against real AWS S3

Set no endpoint — the SDK resolves the regional one, and pinning a host is
how a suite outlives the service it tests:

```bash
AWS_PROFILE=… PALADIN_CONFORMANCE_PROVIDER=aws PALADIN_CONFORMANCE_REGION=eu-north-1   go test -tags=conformance ./tests/conformance/ -v
```

Two defaults flip when no endpoint is given, because they are wrong for AWS
and right for everything self-hosted here: path-style addressing goes off (AWS
serves virtual-hosted URLs and has been retiring the other form for years),
and auth falls back to the SDK's own credential chain — profile, SSO, instance
role — which is how anyone actually reaches AWS. Supplying
`PALADIN_CONFORMANCE_ACCESS_KEY`/`_SECRET_KEY` still selects static keys.

It creates a bucket, writes a handful of small objects, and removes both. On
S3 that is a few cents at most, but it IS a real bucket in a real account:
point it at a scratch account, not production.

## Why it drives the adapter, not the SDK

The contract is not "S3" in the abstract, it is the eighteen methods
`s3adapter.Client` exposes and the handlers call. Testing the SDK directly
would pin what AWS does; testing the adapter pins what Paladin needs, including
the parts the adapter itself decides — key composition, presign host, the
not-found classification.

## Two kinds of assertion

**Required** — Paladin cannot function without it, so a failure is a failure:
create/head a bucket, presign a PUT the caller can actually use, HEAD the
object back, multipart round trip, copy, stream, delete.

**Capability** — legitimately varies between implementations, so it is
*recorded*, not demanded: bucket tagging, whether a bodiless 404 carries an
error code, sequencer on HEAD, checksum algorithms, conditional writes.
These print as a profile table.

The distinction is the whole point. Divergence that fails a build gets
special-cased in code until nobody knows what is required; divergence that is
measured and written down can be designed around once, in one place.

## Measured profiles (2026-08-31)

Run against the three backends this deployment uses. Every **required** check
passed on all three — Paladin runs on any of them.

| capability | MinIO | Garage | SeaweedFS |
|---|---|---|---|
| `bucket.tagging` | yes | **no** (501) | yes |
| `bucket.delete_refuses_nonempty` | yes (409) | yes (409) | **no** |
| `head.missing_bucket_not_notfound` | yes | yes | yes |
| `head.checksum` | no | no | no |
| `head.sequencer` | no | no | no |
| `presign.checksum.SHA256` | yes | yes | yes |
| `presign.checksum.CRC32C` | yes | yes | yes |
| `presign.post` | yes | yes | yes |

Two of these matter, and they are the reason the suite exists.

**SeaweedFS deletes a non-empty bucket.** MinIO and Garage answer 409 and
refuse; SeaweedFS removes it and the objects with it. Paladin's own referential
guard (`bucketh.DeleteBucket` counts every relation holding the bucket) is
therefore not defence in depth on SeaweedFS — it is the only thing there. On
the other two the backend is a second line that would catch a regression in
that guard. It is the deployment's `secondary` backend.

**Garage has no PutBucketTagging.** Already designed around: the bucket
reconciler treats cost-attribution tagging as best-effort and logs a warning,
with a comment saying a backend that lacks it must not block provisioning.
What changed is that this is now measured rather than asserted — the comment
said it, the suite shows it.

`head.checksum` and `head.sequencer` are empty everywhere, which matches the
adapter's own note ("SeaweedFS / real S3: no Sequencer from HEAD; leave
empty"). Anything downstream that wants them has to get them from the event
pipeline, not from a HEAD.

Not yet run against real AWS S3 — no credentials were available on the machine
where the other three were measured. That profile is the one most worth having,
since it is the only backend here whose behaviour nobody can inspect by reading
a container's source, and it is the one that decides whether the two findings
above are quirks of self-hosted implementations or the shape of S3 itself.
