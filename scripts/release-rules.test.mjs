// release-rules.test.mjs — which release each commit type cuts, through the
// commit analyzer and the plugin config release.config.cjs gives it.
//
// A type that releases nothing is silent: the release workflow succeeds and no
// version is cut. That is how a security fix stayed unreleased. Each case
// below pins one type.
//
// Runs from `task -t Taskfile.dev.yaml verify-repo` after `npm ci`.

import { createRequire } from "node:module";
import { analyzeCommits } from "@semantic-release/commit-analyzer";

const require = createRequire(import.meta.url);
const config = require("../release.config.cjs");
const [, pluginConfig] = config.plugins.find(
  (p) => Array.isArray(p) && p[0] === "@semantic-release/commit-analyzer",
);

const cases = [
  ["feat(api): add a thing", "minor"],
  ["fix(api): repair a thing", "patch"],
  ["perf(api): speed a thing up", "patch"],
  ["security(audit): stop storing credentials", "patch"],
  ["feat(api)!: drop a field", "major"],
  ["docs: explain a thing", null],
  ["ci: run a check", null],
  ["chore: tidy", null],
  ["refactor(api): move a thing", null],
];

const logger = { log() {}, error() {}, warn() {}, success() {} };
let failed = 0;
for (const [message, want] of cases) {
  const got = await analyzeCommits(pluginConfig, {
    commits: [{ hash: "0", message }],
    logger,
    cwd: process.cwd(),
    options: {},
  });
  if ((got ?? null) !== want) {
    console.log(`FAIL ${JSON.stringify(message)}: got ${got ?? null}, want ${want}`);
    failed = 1;
  }
}
if (failed) process.exit(1);
console.log("release rules: every commit type cuts the release it should");
