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
publishes an image and a chart for each component at that tag. When the
commits since the last tag are only docs, refactor, test, build, ci or chore,
no release is due and nothing is published.

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
