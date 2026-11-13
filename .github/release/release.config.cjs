// Computes the next SemVer from Conventional Commits and creates the git tag —
// nothing else. Run by .github/workflows/release.yaml, which ci.yaml dispatches
// once every check on a push to main has passed.
//
// Two release streams, chosen by RELEASE_STREAM:
//
//   product  v<version>              backend and frontend images and charts
//   sdk      sdk/python/v<version>   both SDKs, at one version (ADR-0043):
//            sdk/go/v<version>       the same version for the Go module, which
//                                    the Go proxy needs under its directory
//
// Each releases from the commits that touch its own files, read by
// paths-analyzer.mjs: the SDKs for a change under either SDK's directory,
// proto/ or the tables both are tested against (sdk/testdata/), the product
// for any commit that changes what its images are built from — which is all
// of it but the Python SDK: the backend compiles sdk/go/ through `replace`,
// and its Dockerfile copies it. A commit that touches both streams releases
// each.
//
// semantic-release cuts the sdk stream's tag, sdk/python/v<version>: that
// stream was ahead of the Go one when they were joined, so every version it
// cuts is new to both. .github/ci/mirror-tag.ts then tags the Go module at the
// same version on the same commit. The capability primitive is github.com/oleg-tkachuk/limes, which
// releases from its own repository.
//
// The tag IS the release. The GitHub release beside the product's is notes for
// people, created by the workflow with generated notes (.github/ci/announce.ts)
// and shaped by .github/release.yml — so no @semantic-release/github here.
//
// `conventionalcommits` preset: feat is a minor, fix/perf/revert are a patch,
// a `!` or `BREAKING CHANGE:` footer is a major, and
// docs/style/refactor/test/build/ci/chore release nothing on their own.
// On the sdk stream a build(deps) commit that changes what an SDK's users
// install is a patch too (DEPENDENCY_BUMP_IS_A_PATCH).
// `security` is a patch on every stream: the preset knows no such type, so a
// security fix released nothing. Both streams are pre-1.0, where a breaking
// change is a minor; each says so until it reaches 1.0, which is cut by hand.
//
// RELEASE_BUMP is a release an operator asks for by hand — `minor` or
// `major` — cut whatever the commits say (release.yaml's `bump` input). From
// 0.x a major is 1.0.0. Once a stream is past 1.0, drop its
// BREAKING_IS_A_MINOR_BEFORE_1_0, and a major comes from a breaking commit or
// from this alone. release-rules.test.mjs pins all of it.

const ANALYZER = "./paths-analyzer.mjs";
const PRESET = "conventionalcommits";
const SECURITY_IS_A_PATCH = { type: "security", release: "patch" };
const BREAKING_IS_A_MINOR_BEFORE_1_0 = { breaking: true, release: "minor" };

// What both SDKs are generated from and held to.
const SHARED_SDK_PATHS = ["proto/", "sdk/testdata/"];
// The Python SDK's own files: its directory, and the version git archive
// writes into .git_archival.txt for a build from a source archive. The file
// sits at the root because git archive fills it in there; only hatch-vcs
// reads it.
const PYTHON_SDK_OWN = ["sdk/python/", ".git_archival.txt"];
const SDK_PATHS = ["sdk/go/", ...PYTHON_SDK_OWN, ...SHARED_SDK_PATHS];
// The Go module the backend compiles in: its API is its own, not the product's.
const MODULE_PATHS = ["sdk/go/"];
// What a user of either SDK installs with it: the published Go module's
// requirements and the Python package's. Not the lock file or the test
// module, which no user installs.
const SDK_MANIFESTS = ["sdk/go/go.mod", "sdk/python/pyproject.toml"];
// A dependency bump the SDKs ship is a patch of theirs: a user who takes a
// newer limes otherwise holds a Go SDK that requires, and was built against,
// the older one. Renovate writes such bumps as fix(deps); one written by hand
// as build(deps) released nothing.
const DEPENDENCY_BUMP_IS_A_PATCH = { type: "build", scope: "deps", release: "patch" };
// Files that do not decide which stream a commit belongs to, when it changed
// code too: its upgrade notes and READMEs follow the code.
const DOCUMENTATION = { prefixes: ["docs/"], suffixes: [".md"] };

const streams = {
  product: {
    // Restarted at 0.x under this prefix (ADR-0045): semantic-release reads
    // only tags of tagFormat, and v1.0.0…v42.1.0 would outrank any 0.x.
    tagFormat: "release/v${version}",
    // The numbering before the restart, still named in the upgrade guide.
    legacyTagFormats: ["v${version}"],
    // Only the Python SDK is in no image. sdk/go/ is in the backend's, and
    // proto/ is the server's contract. A breaking change to the Go SDK's own
    // API alone is not the product's: it releases the product as a minor, the
    // way it releases the SDK.
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      exclude: PYTHON_SDK_OWN,
      modules: { paths: MODULE_PATHS, releaseRules: [BREAKING_IS_A_MINOR_BEFORE_1_0] },
      documentation: DOCUMENTATION,
    },
  },
  // Both SDKs. A change to either releases both, at one version: a consumer
  // of the two reads one number, and the notes list what changed in each.
  sdk: {
    tagFormat: "sdk/python/v${version}",
    // The Go module's tag of the same release, which semantic-release does
    // not cut: .github/ci/mirror-tag.ts does.
    goModuleTagFormat: "sdk/go/v${version}",
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      include: SDK_PATHS,
      manifests: { files: SDK_MANIFESTS, releaseRules: [DEPENDENCY_BUMP_IS_A_PATCH] },
      documentation: DOCUMENTATION,
    },
  },
};

const stream = process.env.RELEASE_STREAM || "product";
if (!streams[stream]) {
  throw new Error(`RELEASE_STREAM=${stream}: not one of ${Object.keys(streams).join(", ")}`);
}

// The release asked for by hand, if any; empty or "auto" is none.
const AUTO_BUMP = "auto";
const bump = process.env.RELEASE_BUMP || AUTO_BUMP;

/** The analyzer's options for one stream, with the release asked for by hand. */
const analyzerFor = (name, atLeast = bump === AUTO_BUMP ? undefined : bump) => ({
  preset: PRESET,
  ...streams[name].analyzer,
  ...(atLeast === undefined ? {} : { atLeast }),
});

module.exports = {
  branches: ["main"],
  tagFormat: streams[stream].tagFormat,
  repositoryUrl: "https://github.com/oleg-tkachuk/paladin.git",
  plugins: [[ANALYZER, analyzerFor(stream)]],
  // Read by release-rules.test.mjs; semantic-release ignores them.
  streams,
  analyzerFor,
};
