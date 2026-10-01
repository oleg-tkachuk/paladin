# How changes land

`main` is the only long-lived branch. Changes reach it through pull requests,
with [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
The commit type is what decides the next release, so a subject that does not
follow the format releases nothing.

[`ci.yaml`](../.github/workflows/ci.yaml) runs the groups of `verify-all` a
change can reach — `verify-backend`, `verify-capability`, `verify-sdk`,
`verify-frontend` and `verify-repo`, one job each — and audits the workflows
with actionlint and zizmor. [`scripts/ci-groups.sh`](../scripts/ci-groups.sh)
decides which groups a set of paths reaches; a console-only change does not
run the backend's tests. Run by hand, it runs every group. A final `All checks
passed` job is the one check branch protection requires, so a group that was
skipped does not block a merge and a group that failed does.
[`codeql.yaml`](../.github/workflows/codeql.yaml) runs CodeQL on the Go,
TypeScript, Python and workflow code.

A change that touches only documentation (Markdown, `docs/`, images, the
licence files) runs no verify group and no CodeQL analysis, and does not
dispatch a release. Its commits count towards the next release a code change
triggers.

Any other green push to `main` dispatches
[`release.yaml`](../.github/workflows/release.yaml). semantic-release computes
the next tag from the commits since the last one; both images and both charts
are pushed to GHCR at that version, and the GitHub release with generated
notes is created once all of them are. The SDK and API-contract tags follow their own
stream; see [releasing.md](releasing.md).

`ci.yaml` pushes no image and deploys nothing. Images and charts are published
only by `release.yaml`, for a release tag. Its End-to-end job runs `verify-e2e`
— Playwright against a stack built from the change — when the change reaches
the console or the backend, and its Deep job runs `verify-deep` — the
Postgres-backed integration suites and the stack gate — when the change reaches
the backend. The `All checks passed` check that `main` requires waits for both;
[task.md](task.md#working-on-the-code) lists them for running locally.
