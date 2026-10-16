# ADR-0034: The product releases as a train; the dev cluster follows dev builds

- **Status:** Accepted — implemented 2026-10-16 (`release.yaml` runs by hand
  for the product, `publish-plan.sh`, `publish-version-check.sh`,
  `release-ci-check.sh`).
- **Related:** [releasing.md](../releasing.md),
  [how-changes-land.md](../how-changes-land.md).

- **Context.** Every green push to `main` cut a product release, so the
  version counted merges rather than lines of compatible behaviour: 34 majors
  in seventeen days, up to five a day, each with its own section in
  `upgrading.md`. Two breaking pull requests in one day made two majors, and a
  consumer reading the version learned only that something had merged. The
  per-merge release also did a second job: it was how the dev cluster, which
  follows every published chart, got a change at all.

- **Decision.**
  1. **The product is released by hand, as a train.** `release.yaml` runs the
     product stream only when run from the Actions tab (`workflow_dispatch`).
     semantic-release computes the tag from every commit since the last
     release, so the train's breaking changes make one major, its features
     one minor. A manual run tags main's head only once a push run of
     `ci.yaml` on `main` concluded success for that commit; an untested head
     is refused, which is what being dispatched by CI guaranteed before.
  2. **Every green commit on `main` is published as a dev build.** The run CI
     dispatches publishes both images and both charts at the version the task
     library derives for an untagged commit, `<next patch>-dev.<committer
     time>.g<sha>`: after the last release, before the next. It is signed
     like a release, gets no GitHub release, and moves no floating tag. A
     publish whose version does not fit its kind is refused.
  3. **The dev cluster follows dev builds.** Its charts track a constraint
     that admits prereleases (`>=0.0.0-0`), so it runs each change minutes
     after it merges and takes each release in the same sequence.
  4. **The SDK stream is unchanged.** It is pre-1.0, where a breaking change is
     a minor, and its consumers need its changes as they land; CI's run still
     cuts `sdk/go/v*` when a commit touched it.
  5. **A `!` marks only a change a deployment or a client must act on:** wire
     removals and renames, a field that became required, a chart or
     configuration key an existing file no longer works with, a migration that
     needs an operator or drops data. A fix to a wrong answer is a `fix` with an
     upgrade note; console pages, URLs and additive fields are not breaking
     ([releasing.md](../releasing.md#what-is-a-breaking-change)).

- **Consequences.**
  - The dev cluster runs `main`, not a release. A breaking change to the
    chart's values or the configuration schema reaches it minutes after its
    pull request merges, so the matching change in the deployment repository
    lands before that merge, not before the release.
  - A red or flaky CI run on `main` now holds back the dev cluster, not only a
    release: the commit is published once its run is green.
  - `upgrading.md`'s unreleased sections accumulate for a train and are named
    by the release that ships them.
  - GHCR holds a dev image and chart per green merge. Their retention is
    deferred work (BACKLOG).
