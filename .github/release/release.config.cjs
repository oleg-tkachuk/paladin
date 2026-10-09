// Computes the next SemVer from Conventional Commits and creates the git tag —
// nothing else. Run by .github/workflows/release.yaml, which ci.yaml dispatches
// once every check on a push to main has passed.
//
// Three release streams, one tag format each, chosen by RELEASE_STREAM:
//
//   product     v<version>            backend and frontend images and charts
//   sdk         sdk/go/v<version>     the Go and Python SDKs (Go needs the prefix)
//   capability  capability/v<version> the capability module
//
// Each releases from the commits that touch its own files, read by
// paths-analyzer.mjs: the SDK for a change under sdk/ or
// proto/, the capability module for one under capability/, the product for
// any commit that changes what its images are built from — which is all of
// it but the Python SDK: the backend compiles capability/ and sdk/go/ through
// `replace`, and its Dockerfile copies both. A commit that touches several
// streams releases each.
//
// The tag IS the release. The GitHub release beside the product's is notes for
// people, created by the workflow with `gh release create --generate-notes`
// and shaped by .github/release.yml — so no @semantic-release/github here.
//
// `conventionalcommits` preset: feat is a minor, fix/perf/revert are a patch,
// a `!` or `BREAKING CHANGE:` footer is a major, and
// docs/style/refactor/test/build/ci/chore release nothing on their own.
// `security` is a patch on every stream: the preset knows no such type, so a
// security fix released nothing. The SDK and the capability module are pre-1.0,
// where a breaking change is a minor; their streams say so until they reach
// 1.0. release-rules.test.mjs pins all of it.

const ANALYZER = "./paths-analyzer.mjs";
const PRESET = "conventionalcommits";
const SECURITY_IS_A_PATCH = { type: "security", release: "patch" };
const BREAKING_IS_A_MINOR_BEFORE_1_0 = { breaking: true, release: "minor" };

const SDK_PATHS = ["sdk/", "proto/"];
const CAPABILITY_PATHS = ["capability/"];
// The Go modules the backend compiles in: their API is theirs, not the product's.
const MODULE_PATHS = ["sdk/go/", ...CAPABILITY_PATHS];
// Files that do not decide which stream a commit belongs to, when it changed
// code too: its upgrade notes and READMEs follow the code.
const DOCUMENTATION = { prefixes: ["docs/"], suffixes: [".md"] };

const streams = {
  product: {
    tagFormat: "v${version}",
    // Only sdk/python/ is in no image. capability/ and sdk/go/ are in the
    // backend's, and proto/ is the server's contract. A breaking change to a
    // module's own API alone is not the product's: it releases the product
    // as a minor, the way it releases the module.
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH],
      exclude: ["sdk/python/"],
      modules: { paths: MODULE_PATHS, releaseRules: [BREAKING_IS_A_MINOR_BEFORE_1_0] },
      documentation: DOCUMENTATION,
    },
  },
  sdk: {
    tagFormat: "sdk/go/v${version}",
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      include: SDK_PATHS,
      documentation: DOCUMENTATION,
    },
  },
  capability: {
    tagFormat: "capability/v${version}",
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      include: CAPABILITY_PATHS,
      documentation: DOCUMENTATION,
    },
  },
};

const stream = process.env.RELEASE_STREAM || "product";
if (!streams[stream]) {
  throw new Error(`RELEASE_STREAM=${stream}: not one of ${Object.keys(streams).join(", ")}`);
}

/** The analyzer's options for one stream. */
const analyzerFor = (name) => ({ preset: PRESET, ...streams[name].analyzer });

module.exports = {
  branches: ["main"],
  tagFormat: streams[stream].tagFormat,
  repositoryUrl: "https://github.com/oleg-tkachuk/paladin.git",
  plugins: [[ANALYZER, analyzerFor(stream)]],
  // Read by release-rules.test.mjs; semantic-release ignores them.
  streams,
  analyzerFor,
};
