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
run the backend's tests. Run by hand, it runs every group.

A green push to `main` dispatches
[`release.yaml`](../.github/workflows/release.yaml). semantic-release computes
the next tag from the commits since the last one, and a GitHub release with
generated notes is created for it. Both images and both charts are then pushed
to GHCR at that version. The SDK and API-contract tags follow their own
stream; see [releasing.md](releasing.md).

`ci.yaml` builds no image and deploys nothing. Images and charts are published
only by `release.yaml`, for a release tag. `verify-deep` and `verify-e2e` need
Docker, so they run locally before a merge; [task.md](task.md#working-on-the-code)
lists them.
