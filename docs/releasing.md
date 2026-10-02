# Releasing

Three release streams and one baseline. Each stream tracks what its
consumers actually depend on, and each is cut automatically from the commits
that touch it ([`release.config.cjs`](../release.config.cjs)).

| Tag | Cut by | Publishes | Versioned by |
| --- | --- | --- | --- |
| `vX.Y.Z` | semantic-release, after a green push to `main` | both images and both Helm charts, plus the GitHub release | the commits that change what the images are built from — everything but `sdk/python/` |
| `sdk/go/vX.Y.Z` | semantic-release, in the same run | the tag, which the Go proxy and pip resolve, and a GitHub release | the commits that touch `sdk/` or `proto/` |
| `capability/vX.Y.Z` | semantic-release, in the same run | the tag, for `go get`, and a GitHub release | the commits that touch `capability/` |
| `api/vX.Y.Z` | a maintainer, by hand | nothing; the baseline `buf breaking` compares against | the API contract |

The module releases' notes list only their own `feat` and `fix` commits
(`scripts/stream-release-notes.sh`), and none is marked latest: that stays the
product's release.

A commit counts for every stream whose files it touches, and its type
decides the bump on each. The backend compiles `capability/` and `sdk/go/`
through `replace` and its image copies both, so a change there releases the
product as well as its own module — `feat` a minor, `fix`/`perf`/`security` a patch.
The SDK and the capability module are pre-1.0, so a breaking change is a
minor on their streams until they reach 1.0. A breaking change confined to
`sdk/go/` or `capability/` breaks that module's API, not the product's, so it
releases the product as a minor too; one that also touches the server is the
product's major.

## The product: `vX.Y.Z`

The backend and the console ship under one tag, and neither has a version of
its own. They are built from one commit, and the console calls the Connect
contract of that same commit, so any other pairing is one nobody has tested.
Separate versions would add a compatibility matrix, and a change seldom
touches one side alone.

Nothing is tagged by hand. `ci.yaml` dispatches
[`release.yaml`](../.github/workflows/release.yaml) once every check on
`main` has passed. semantic-release computes the next version from the commit
types (see [`release.config.cjs`](../release.config.cjs)), and the workflow
publishes an image and a chart for each component at that tag. Only the
commit whose checks dispatched the run is released: if `main` has moved on in
the meantime, that run releases nothing, and the newer commit's own checks
release both. The GitHub
release is created last, and only when every image and chart was pushed; if a
push fails, the tag stays without a release until the failed jobs are re-run.
`feat` is a minor release, `fix`, `perf` and `security` a patch, a breaking
change (`!` or a `BREAKING CHANGE:` footer) a major. Any other type — `docs`,
`style`, `refactor`, `test`, `build`, `ci`, `chore` — releases nothing on its
own.

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

The logic is [`scripts/release-sign.sh`](../scripts/release-sign.sh) and
[`scripts/release-sign-chart.sh`](../scripts/release-sign-chart.sh);
`task -t Taskfile.dev.yaml verify:release-signing` runs both with a throwaway
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

## The SDKs: `sdk/go/vX.Y.Z`

The SDKs version on their own, from the commits that touch `sdk/` or
`proto/` — a contract change regenerates the stubs, whatever its commit's
scope. One version covers both languages: the Go module resolves
`sdk/go/vX.Y.Z`, and the Python package reads the same tag at build time
through hatch-vcs, so neither carries a version to edit. A release is the
tag; nothing else is published, and the Python package is not on PyPI yet.

`api/vX.Y.Z` is no longer a release. It is the baseline `buf breaking`
compares against, cut by hand when a deliberate contract change has landed —
see [upgrading.md](upgrading.md#changing-the-api-contract).

## The capability module: `capability/vX.Y.Z`

`capability/` is its own Go module with its own stream, cut from the commits
that touch it. Paladin still uses it through a `replace` directive, so its
releases do not wait on these tags; they are for `go get` from outside. The
wire-format promise, and why the product's tag cannot be mirrored, are in
[capability/README.md](../capability/README.md#versioning).
