// Computes the next SemVer from Conventional Commits and creates the git tag —
// nothing else. Run by .github/workflows/release.yaml, which ci.yaml dispatches
// once every check on a push to main has passed.
//
// The tag IS the release. The GitHub release beside it is notes for people,
// created by the workflow with `gh release create --generate-notes` and shaped
// by .github/release.yml — so no @semantic-release/github here, which would
// create the same release first and make that step collide with it.
//
// `conventionalcommits` preset: feat is a minor, fix/perf/revert are a patch,
// a `!` or `BREAKING CHANGE:` footer is a major, and
// docs/style/refactor/test/build/ci/chore release nothing on their own.
//
// One rule on top: `security` is a patch. The preset knows no such type, so a
// security fix released nothing and stayed out of every image until an
// unrelated fix came along. scripts/release-rules.test.mjs pins this.
module.exports = {
  branches: ["main"],
  tagFormat: "v${version}",
  repositoryUrl: "https://github.com/oleg-tkachuk/paladin.git",
  plugins: [
    [
      "@semantic-release/commit-analyzer",
      {
        preset: "conventionalcommits",
        // Added to the preset's rules, which still decide every other type.
        releaseRules: [{ type: "security", release: "patch" }],
      },
    ],
  ],
};
