// Computes the next SemVer from Conventional Commits and creates the git tag —
// nothing else. Run by .github/workflows/release.yaml, which ci.yaml dispatches
// once every check on a push to main has passed.
//
// Three release streams, one tag format each, chosen by RELEASE_STREAM:
//
//   product     v<version>                backend and frontend images and charts
//   go-sdk      sdk/go/v<version>         the Go SDK (the Go proxy needs the prefix)
//   python-sdk  sdk/python/v<version>     the Python SDK, which hatch-vcs reads
//
// Each releases from the commits that touch its own files, read by
// paths-analyzer.mjs: an SDK for a change under its own directory, proto/ or
// the tables both SDKs are tested against (sdk/testdata/), the product
// for any commit that changes what its images are built from — which is all
// of it but the Python SDK: the backend compiles sdk/go/ through `replace`,
// and its Dockerfile copies it. A commit that touches both streams releases
// each. The capability primitive is github.com/oleg-tkachuk/limes, which
// releases from its own repository.
//
// The tag IS the release. The GitHub release beside the product's is notes for
// people, created by the workflow with generated notes (.github/ci/announce.ts)
// and shaped by .github/release.yml — so no @semantic-release/github here.
//
// `conventionalcommits` preset: feat is a minor, fix/perf/revert are a patch,
// a `!` or `BREAKING CHANGE:` footer is a major, and
// docs/style/refactor/test/build/ci/chore release nothing on their own.
// `security` is a patch on every stream: the preset knows no such type, so a
// security fix released nothing. The SDK is pre-1.0, where a breaking change
// is a minor; its stream says so until it reaches 1.0. release-rules.test.mjs pins all of it.

const ANALYZER = "./paths-analyzer.mjs";
const PRESET = "conventionalcommits";
const SECURITY_IS_A_PATCH = { type: "security", release: "patch" };
const BREAKING_IS_A_MINOR_BEFORE_1_0 = { breaking: true, release: "minor" };

// What both SDKs are generated from and held to.
const SHARED_SDK_PATHS = ["proto/", "sdk/testdata/"];
const GO_SDK_PATHS = ["sdk/go/", ...SHARED_SDK_PATHS];
// The Python SDK's own files: its directory, and the version git archive
// writes into .git_archival.txt for a build from a source archive. The file
// sits at the root because git archive fills it in there; only hatch-vcs
// reads it.
const PYTHON_SDK_OWN = ["sdk/python/", ".git_archival.txt"];
const PYTHON_SDK_PATHS = [...PYTHON_SDK_OWN, ...SHARED_SDK_PATHS];
// The Go module the backend compiles in: its API is its own, not the product's.
const MODULE_PATHS = ["sdk/go/"];
// Files that do not decide which stream a commit belongs to, when it changed
// code too: its upgrade notes and READMEs follow the code.
const DOCUMENTATION = { prefixes: ["docs/"], suffixes: [".md"] };

const streams = {
  product: {
    tagFormat: "v${version}",
    // Only the Python SDK is in no image. sdk/go/ is in the backend's, and
    // proto/ is the server's contract. A breaking change to the Go SDK's own
    // API alone is not the product's: it releases the product as a minor, the
    // way it releases the SDK.
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH],
      exclude: PYTHON_SDK_OWN,
      modules: { paths: MODULE_PATHS, releaseRules: [BREAKING_IS_A_MINOR_BEFORE_1_0] },
      documentation: DOCUMENTATION,
    },
  },
  "go-sdk": {
    tagFormat: "sdk/go/v${version}",
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      include: GO_SDK_PATHS,
      documentation: DOCUMENTATION,
    },
  },
  // Its first tag was cut by hand at the Go SDK's last shared version
  // (docs/releasing.md): with no tag of its own, semantic-release starts at 1.0.0.
  "python-sdk": {
    tagFormat: "sdk/python/v${version}",
    analyzer: {
      releaseRules: [SECURITY_IS_A_PATCH, BREAKING_IS_A_MINOR_BEFORE_1_0],
      include: PYTHON_SDK_PATHS,
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
