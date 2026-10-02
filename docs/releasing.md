# Releasing

Three tag families, three version streams. Each tracks what its consumers
actually depend on.

| Tag | Cut by | Publishes | Versioned by |
| --- | --- | --- | --- |
| `vX.Y.Z` | semantic-release, after a green push to `main` | both images and both Helm charts, plus the GitHub release | the product: Conventional Commit types since the last tag |
| `api/vX.Y.Z` | a maintainer, by hand | the proto baseline; `sdk/go/vX.Y.Z` on the same commit | the API contract |
| `capability/vX.Y.Z` | nobody for now | — | — |

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

## The SDKs: `api/vX.Y.Z`

The Go SDK is generated from the proto, so it follows the contract, not the
product. A client cares whether the RPCs it calls have changed, not whether
the server shipped a fix. Cutting a contract baseline, described in
[upgrading.md](upgrading.md#changing-the-api-contract), is also what releases
the SDK. [`sdk.yaml`](../.github/workflows/sdk.yaml) tags `sdk/go/vX.Y.Z`
on the same commit, which is the tag the Go proxy resolves for the
`sdk/go` module path. Never push `sdk/go/…` by hand.

The Python SDK carries the same contract version in
`sdk/python/pyproject.toml` and is not published anywhere yet.

## The capability module

`capability/` is its own Go module, but it has no release stream. Paladin
uses it through a `replace` directive, so no Paladin release depends on a
capability tag. The one existing tag, `capability/v0.1.0`, is there for
hygiene. Why mirroring the product tag cannot work, and what would justify
a pipeline of its own, is in
[capability/README.md](../capability/README.md#versioning).
