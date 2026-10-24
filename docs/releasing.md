# Releasing

Three release streams and one baseline. Each stream tracks what its
consumers actually depend on, and each is versioned from the commits
that touch it ([`release.config.cjs`](../.github/release/release.config.cjs)).
The SDK's tag is cut automatically; the product's when a maintainer runs the
release workflow.

| Tag | Cut by | Publishes | Versioned by |
| --- | --- | --- | --- |
| `vX.Y.Z` | semantic-release, when `release.yaml` is run by hand | both images and both Helm charts, plus the GitHub release | the commits that change what the images are built from — everything but `sdk/python/` |
| `sdk/go/vX.Y.Z` | semantic-release, after a green push to `main` | the tag, which the Go proxy resolves, and a GitHub release | the commits that touch `sdk/go/`, `proto/` or `sdk/testdata/` |
| `sdk/python/vX.Y.Z` | semantic-release, after a green push to `main` | the tag, which a git install of the package resolves, and a GitHub release | the commits that touch `sdk/python/`, `proto/` or `sdk/testdata/` |
| `api/vX.Y.Z` | a maintainer, by hand | nothing; the baseline `buf breaking` compares against | the API contract |

The SDK release's notes list only their own `feat`, `fix`, `perf`,
`security` and `revert` commits ([`sdk-release.ts`](../.github/ci/sdk-release.ts)), and
none is marked latest: that stays the product's release.

A commit counts for every stream whose files it touches, and its type
decides the bump on each. The backend compiles `sdk/go/` through `replace`
and its image copies it, so a change there releases the product as well as
the SDK — `feat` a minor, `fix`/`perf`/`revert`/`security` a patch. The SDK is
pre-1.0, so a breaking change is a minor on its stream until it reaches 1.0.
A breaking change confined to `sdk/go/` breaks the SDK's API, not the
product's, so it releases the product as a minor too; one that also touches the server is the
product's major. Documentation does not decide where a commit belongs:
a breaking SDK commit that also writes its section of upgrading.md is still
the SDK's alone.

## The product: `vX.Y.Z`

The backend and the console ship under one tag, and neither has a version of
its own. They are built from one commit, and the console calls the Connect
contract of that same commit, so any other pairing is one nobody has tested.
Separate versions would add a compatibility matrix, and a change seldom
touches one side alone.

The product is released by hand, as a train
([ADR-0034](adr/0034-the-product-releases-as-a-train.md)). Run
[`release.yaml`](../.github/workflows/release.yaml) from the Actions tab, or
`gh workflow run release.yaml`: it tags main's head once CI passed on that
commit ([`release-head.ts`](../.github/ci/release-head.ts)), and
refuses an untested one. semantic-release computes the version from every
commit since the last release (see
[`release.config.cjs`](../.github/release/release.config.cjs)), so a train of
several breaking changes is one major. The workflow then publishes an image and
a chart for each component at that tag. The GitHub
release is created last, and only when every image and chart was pushed; if a
push fails, the tag stays without a release until the failed jobs are re-run.

A release candidate goes the same way with the `prerelease` channel —
`gh workflow run release.yaml -f channel=prerelease`. It tags the version the
release would take as `vX.Y.Z-rc.N`, one past the last candidate of that
version ([`prerelease.ts`](../.github/ci/prerelease.ts)), publishes its images
and charts at `X.Y.Z-rc.N`, and announces it as a prerelease that is never the
Latest. semantic-release counts only stable tags, so the release that follows
the candidates is still `vX.Y.Z`.
Between releases every green commit is published as a dev build (below), so
nothing waits on a release to reach the dev cluster.

`feat` is a minor release, `fix`, `perf`, `revert` and `security` a patch, a breaking
change (`!` or a `BREAKING CHANGE:` footer) a major. Any other type — `docs`,
`style`, `refactor`, `test`, `build`, `ci`, `chore` — releases nothing on its
own.

A proto's comments are documentation: a change to them alone is
`docs(proto)`, even when the server's own `feat` is what they describe.
Commit the two apart. A `feat` that touches only a comment under `proto/`
cuts an SDK release whose code is unchanged, which every consumer then has to
read to find nothing in it. CI refuses such a commit in its pull request: one
that would cut a release, changes `proto/`, and leaves the contract compiled
without its comments as it was (`.github/ci/proto-comments.ts`).

### What is a breaking change

A `!` makes the train a major, so it marks only a change a deployment or a
client **must act on**:

- a field, RPC or enum value removed or renamed on the wire (`buf breaking`
  reports it);
- a request field that was optional and is now required;
- a chart value or configuration key whose old spelling an existing
  `values.yaml` or config file no longer works with;
- a migration that needs an operator to do something, or drops data that
  cannot be recovered.

A change that only stops a wrong answer is a `fix`, with an `upgrading.md`
note saying what a client will see differently. Console pages and URLs,
additive fields and RPCs, and new behaviour behind a new field are not
breaking.

### Dev builds

In the Actions tab each `release.yaml` run is named for what it releases:
`DevRelease v39.2.1-dev (622e006)` for a dev build, as the console labels it
(the short commit alone when CI could not derive the version), `PreRelease` for a release candidate and `Release` for a
release. A run's name is fixed when it starts, before semantic-release derives
a release's version, so those two carry none; the GitHub release names it.

Every commit that passed CI on `main` is published as a dev build: an image and a chart at the version the task library derives for an
untagged commit, `<next patch>-dev.<committer time>.g<sha>`. Such a version
sorts after the last release and before the next one, so a deployment that
follows prereleases takes each dev build as it lands and the release that
follows it. A dev build gets no GitHub release and moves no floating tag;
[`publish-target.ts`](../.github/ci/publish-target.ts) refuses
one that derived a release version. Once it is pushed and signed, the same job
deletes that component's older dev builds — image and chart, with their
platform manifests, signatures and attestations — from GHCR, keeping only the
newest ([`ghcr-cleanup.ts`](../.github/ci/ghcr-cleanup.ts),
run through `dataaxiom/ghcr-cleanup-action`). Only versions carrying the
`-dev.` mark are matched, so a release is never deleted.

A commit is built from its own sha, so it is published even when `main` has
moved past it by the time its CI passes. The one code commit that gets no dev build
is one whose CI run never finished: GitHub cancels a run still queued when a
newer push to `main` queues behind it, and the newer commit's build carries
its change. A documentation-only push dispatches no release run, so it gets
none either.

### Signatures and SBOMs

Every image and chart a release publishes is signed, and every image carries
a signed SBOM, so a deployment can check what it runs came from this
repository's release workflow and know what is inside it.

- **Signed by digest, keyless.** [Sigstore](https://www.sigstore.dev/) signs
  each image with a short-lived certificate bound to the release workflow's
  GitHub OIDC identity, and records it in the public transparency log. There
  is no key to leak or rotate. The image index and every platform image are
  signed, so a pull by tag and a pull by platform digest both verify.
- **One SBOM per platform.** [Syft](https://github.com/anchore/syft) writes an
  SPDX document for `linux/amd64` and `linux/arm64` separately; each is
  attested (signed in-toto) to its own platform image and attached to the
  GitHub release.
- **Verified before the release exists.** The workflow verifies the
  signatures and attestations it just made, against the identity below, and
  publishes nothing further if they fail.

- **Charts too.** Each chart is signed by digest with the same identity, and
  verified in the same job.

The logic is [`.github/ci/sign.ts`](../.github/ci/sign.ts);
`task -t Taskfile.dev.yaml verify:release-signing` runs it for an image and a chart with a throwaway
key against a local registry.

Signing starts with the first release after 4.11.5; earlier images are
unsigned. To verify an image (cosign v3):

```bash
image=ghcr.io/oleg-tkachuk/paladin-core:4.12.0
cosign verify "$image" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/oleg-tkachuk/paladin/\.github/workflows/release\.yaml@'
```

A chart verifies the same way, by its OCI reference without the `oci://`:
`cosign verify ghcr.io/oleg-tkachuk/charts/paladin-core:4.12.0` with the two
certificate flags above.

The SBOM is attested to a platform image, not to the index a tag names, so
resolve the platform digest first — `cosign verify-attestation` on the tag
finds nothing:

```bash
digest=$(docker buildx imagetools inspect "$image" --format '{{json .Manifest}}' |
  jq -r '.manifests[] | select(.platform.architecture == "amd64") | .digest')
cosign verify-attestation "${image%:*}@${digest}" --type spdxjson \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/oleg-tkachuk/paladin/\.github/workflows/release\.yaml@' |
  jq -r '.payload | @base64d | fromjson | .predicate.packages | length'
```

## The SDKs: `sdk/go/vX.Y.Z` and `sdk/python/vX.Y.Z`

Each SDK versions on its own, from the commits that touch its directory,
`proto/` or `sdk/testdata/` — a contract change regenerates both SDKs' stubs,
whatever its commit's scope, and the shared tables hold both to the server.
A change to one SDK alone releases that one: a release of the other would be
one its consumers read to find nothing in it. Neither carries a version to
edit: the Go module resolves `sdk/go/vX.Y.Z`, and the Python package reads
`sdk/python/vX.Y.Z` at build time through hatch-vcs (and from
`.git_archival.txt` in a source archive). A release is the tag; nothing else
is published, and the Python package is not on PyPI. The release workflow
asks the Go module proxy for each new Go SDK tag
([`.github/ci/sdk-release.ts`](../.github/ci/sdk-release.ts)), so `go get
…@latest` and the README's SDK badge see it at once rather than when the
proxy next refreshes.

The two shared one version until `sdk/go/v0.102.0`; the Python stream's first
tag, `sdk/python/v0.102.0`, was cut by hand on the same commit, and it
versions from there. semantic-release starts a stream that has no tag at
1.0.0, and the release-tag ruleset forbids deleting a tag, so the workflow
releases a stream only once it has one
([`.github/ci/stream-seeded.ts`](../.github/ci/stream-seeded.ts)): a new
stream's first tag is cut by hand, at the version it continues from.

### Behaviour changes

A change that alters what existing code does — a signature, a default, an
error a caller handled — is breaking even when no signature changes: its
commit carries `!` or a `BREAKING CHANGE:` footer, and it gets a section in
[upgrading.md](upgrading.md) headed `## vX.Y.Z — …` for the release that
ships it. Pre-1.0 such a release is only a minor, so its notes list these
commits first, under *Behaviour changes*, with a link to that section
([`.github/ci/sdk-release.ts`](../.github/ci/sdk-release.ts)).

### Which servers an SDK release works with

An SDK release works with every product release built from the contract it
was generated from, or a later one. Within a contract major the contract
only grows — `buf breaking` refuses anything else — so a newer server keeps
answering an older SDK. An older server answers a call it does not have
with `Unimplemented`, which both SDKs raise as a contract-skew error naming
the procedure, the server's release and the SDK's. Only the latest SDK
minor is maintained: a fix lands in the next release, not in an earlier
line.

`api/vX.Y.Z` is no longer a release. It is the baseline `buf breaking`
compares against, cut by hand when a deliberate contract change has landed —
see [upgrading.md](upgrading.md#changing-the-api-contract).

## The capability primitive: limes

The capability primitive was `capability/` here, released as
`capability/vX.Y.Z` (the last is `capability/v0.17.1`). It is now
[limes](https://github.com/oleg-tkachuk/limes), released from its own
repository; `backend/go.mod` requires a limes tag like any dependency, and
Renovate proposes new ones.
