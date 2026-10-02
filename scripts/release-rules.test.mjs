// release-rules.test.mjs — which release each commit cuts, on each release
// stream, through the analyzer and the plugin config release.config.cjs gives
// it.
//
// A commit that releases nothing is silent: the release workflow succeeds and
// no version is cut. That is how a security fix stayed unreleased, and how a
// commit that touched only the SDK republished the product. Each case below
// pins one.
//
// Runs from `task -t Taskfile.dev.yaml verify-repo` after `npm ci`.

import { createRequire } from "node:module";
import { analyzeWith, belongs, deciding, within } from "./release/paths-analyzer.mjs";

const require = createRequire(import.meta.url);
const { streams, analyzerFor } = require("../release.config.cjs");

const BACKEND = ["backend/internal/api/handler.go"];
const SDK = ["sdk/go/paladin/client.go"];
const PYTHON_SDK = ["sdk/python/src/paladin/client.py"];
const PROTO = ["proto/paladin/data/v1/object_service.proto"];
const CAPABILITY = ["capability/mint.go"];
const UPGRADING = ["docs/upgrading.md"];

// [stream, commit message, files it changed, release wanted]
const cases = [
  ["product", "feat(api): add a thing", BACKEND, "minor"],
  ["product", "fix(api): repair a thing", BACKEND, "patch"],
  ["product", "perf(api): speed a thing up", BACKEND, "patch"],
  ["product", "security(audit): stop storing credentials", BACKEND, "patch"],
  ["product", "feat(api)!: drop a field", BACKEND, "major"],
  ["product", "feat(api): a contract change is a server change", PROTO, "minor"],
  ["product", "docs: explain a thing", BACKEND, null],
  ["product", "chore: tidy", BACKEND, null],
  ["product", "refactor(api): move a thing", BACKEND, null],
  // The backend compiles capability/ and sdk/go/ in: a change there changes
  // its image, so the product releases it.
  ["product", "fix(capability): a module fix ships in the backend", CAPABILITY, "patch"],
  ["product", "feat(sdk): the Go SDK is compiled into the backend", SDK, "minor"],
  ["product", "feat(sdk): only the Python SDK", PYTHON_SDK, null],
  ["product", "feat(sdk): the Python SDK and the server", [...PYTHON_SDK, ...BACKEND], "minor"],
  // A module's own breaking change is not a product major.
  ["product", "feat(sdk)!: the Go SDK's API breaks", SDK, "minor"],
  ["product", "feat(capability)!: the module's API breaks", CAPABILITY, "minor"],
  ["product", "feat(sdk)!: the SDK and the server break together", [...SDK, ...BACKEND], "major"],
  ["product", "feat(api)!: a contract break is the server's", PROTO, "major"],
  // Its upgrade notes do not make a module's break the product's: what
  // released v5.0.0 and v6.0.0 for SDK-only breaks.
  ["product", "feat(sdk)!: the Go SDK breaks, with its notes", [...SDK, ...UPGRADING], "minor"],
  ["product", "feat(sdk)!: the Python SDK breaks, with its notes", [...PYTHON_SDK, ...UPGRADING, "sdk/python/README.md"], null],
  ["product", "feat(api)!: the server breaks, with its notes", [...BACKEND, ...UPGRADING], "major"],
  ["product", "docs: documentation alone releases nothing", UPGRADING, null],
  ["sdk", "feat(sdk)!: a break with its notes is still the SDK's", [...SDK, ...UPGRADING], "minor"],

  ["sdk", "feat(sdk): add a helper", SDK, "minor"],
  ["sdk", "fix(sdk): a Python fix", PYTHON_SDK, "patch"],
  ["sdk", "fix(sdk): repair a helper", SDK, "patch"],
  ["sdk", "security(sdk): stop logging a token", SDK, "patch"],
  ["sdk", "feat(events): a new RPC changes the stubs", PROTO, "minor"],
  ["sdk", "feat(sdk)!: before 1.0 a break is a minor", SDK, "minor"],
  ["sdk", "feat(api): only the server", BACKEND, null],
  ["sdk", "docs(sdk): explain a helper", SDK, null],

  ["capability", "feat(capability): add a caveat", CAPABILITY, "minor"],
  ["capability", "fix(capability): repair a check", CAPABILITY, "patch"],
  ["capability", "feat(capability)!: before 1.0 a break is a minor", CAPABILITY, "minor"],
  ["capability", "fix(api): only the server", BACKEND, null],
  ["capability", "feat(sdk): only the SDK", SDK, null],
];

const logger = { log() {}, error() {}, warn() {}, success() {} };
let failed = 0;
for (const [stream, message, files, want] of cases) {
  const got = await analyzeWith(
    analyzerFor(stream),
    { commits: [{ hash: "0", message }], logger, cwd: process.cwd(), options: {} },
    () => files,
  );
  if ((got ?? null) !== want) {
    console.log(`FAIL [${stream}] ${JSON.stringify(message)} on ${files.join(",")}: got ${got ?? null}, want ${want}`);
    failed = 1;
  }
}

// A merge commit changes no files of its own: it belongs to an include
// stream never, and is left to the analyzer (which ignores its message)
// otherwise.
for (const [name, ok] of [
  ["a merge is in no include stream", belongs([], { include: ["sdk/"] }) === false],
  ["a merge is not excluded", belongs([], { exclude: ["sdk/"] }) === true],
  ["include matches a prefix", belongs(["sdk/go/x.go"], { include: ["sdk/"] }) === true],
  ["exclude needs every file", belongs(["sdk/x", "backend/y"], { exclude: ["sdk/"] }) === true],
  ["within needs every file", within(["sdk/go/x", "backend/y"], ["sdk/go/"]) === false],
  ["a merge is within nothing", within([], ["sdk/go/"]) === false],
  ["documentation does not decide", deciding(["sdk/go/a.go", "docs/x.md", "README.md"], { prefixes: ["docs/"], suffixes: [".md"] }).join() === "sdk/go/a.go"],
  ["documentation alone keeps its files", deciding(["docs/x.md"], { prefixes: ["docs/"], suffixes: [".md"] }).join() === "docs/x.md"],
]) {
  if (!ok) {
    console.log(`FAIL ${name}`);
    failed = 1;
  }
}

for (const name of Object.keys(streams)) {
  const tag = streams[name].tagFormat;
  if (!tag.includes("${version}")) {
    console.log(`FAIL [${name}] tagFormat ${tag} has no \${version}`);
    failed = 1;
  }
}

if (failed) process.exit(1);
console.log("release rules: every commit cuts the release it should, on every stream");
