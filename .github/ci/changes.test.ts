import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, test } from "node:test";

import { parse } from "yaml";

import { ALL_GROUPS, DEEP_GROUPS, E2E_GROUPS, INERT, Output, RUNS, SKIPPED, candidateRange, changesSummary, classify, reachedGroups, reachesAny } from "./changes.ts";
import { tempRepo } from "./lib/temp-repo.ts";

// Too few groups and a change merges on checks that never ran against it; too
// many and the split buys nothing. Both are silent in CI, so each case pins
// one decision.
const CASES: [string, string[], string[]][] = [
  ["documentation only", ["README.md", "docs/install.md", "backend/README.md", "docs/diagram.svg"], []],
  ["nothing changed", [], []],
  ["gitignore and licence", [".gitignore", "LICENSE", "NOTICE"], []],
  ["a console file", ["frontend/src/app/page.tsx"], ["frontend", "repo"]],
  ["a backend file", ["backend/internal/mcp/bridge.go"], ["backend", "repo"]],
  ["the Go SDK reaches backend", ["sdk/go/client.go"], ["backend", "sdk", "repo"]],
  ["the Python SDK only", ["sdk/python/pyproject.toml"], ["sdk", "repo"]],
  ["shared SDK fixtures reach their readers", ["sdk/testdata/names.json"], ["backend", "sdk", "repo"]],
  ["the contract reaches all its readers", ["proto/paladin/admin/v1/tenant_service.proto"], [...ALL_GROUPS]],
  ["a chart", ["frontend/deploy/chart/values.yaml"], ["frontend", "repo"]],
  ["root config", [".checkov.yaml"], ["repo"]],
  ["an unclassified path", ["deploy/grafana/dashboard.json"], ["repo"]],
  ["a Taskfile", ["Taskfile.dev.yaml"], [...ALL_GROUPS]],
  ["a cached include", [".task/remote/git.github.com.codegen.yaml"], [...ALL_GROUPS]],
  ["a contract script", ["scripts/chart-values.test.sh"], [...ALL_GROUPS]],
  ["a CI script", [".github/ci/changes.ts"], [...ALL_GROUPS]],
  ["the CI setup action", [".github/actions/setup-ci/action.yml"], [...ALL_GROUPS]],
  ["this workflow", [".github/workflows/ci.yaml"], [...ALL_GROUPS]],
  ["another workflow", [".github/workflows/release.yaml"], ["repo"]],
  ["docs next to code", ["README.md", "frontend/src/app/page.tsx"], ["frontend", "repo"]],
  ["a Markdown file is documentation anywhere", ["frontend/src/notes.md"], []],
  ["an image is documentation anywhere", ["frontend/public/logo.png"], []],
];

describe("reachedGroups", () => {
  for (const [name, paths, want] of CASES) {
    test(name, () => assert.deepEqual(reachedGroups(paths), want));
  }
});

// The Playwright job runs for a change the console or the backend reaches, and
// for nothing else: it is the slowest job here, and the only one that
// exercises the two together.
test("end-to-end runs for the console or the backend", () => {
  assert.equal(reachesAny(["frontend", "repo"], E2E_GROUPS), true);
  assert.equal(reachesAny(["backend", "repo"], E2E_GROUPS), true);
  assert.equal(reachesAny([...ALL_GROUPS], E2E_GROUPS), true);
  assert.equal(reachesAny(["sdk", "repo"], E2E_GROUPS), false);
  assert.equal(reachesAny(["repo"], E2E_GROUPS), false);
  assert.equal(reachesAny([], E2E_GROUPS), false);
});

// verify-deep runs for backend code and for the SDKs, whose scenarios run in
// the stack gate; the contract reaches it through the backend group.
test("deep runs for the backend or an SDK", () => {
  assert.equal(reachesAny(["backend", "repo"], DEEP_GROUPS), true);
  assert.equal(reachesAny(reachedGroups(["sdk/testdata/biscuit/seed.biscuit"]), DEEP_GROUPS), true);
  assert.equal(reachesAny(["sdk", "repo"], DEEP_GROUPS), true);
  assert.equal(reachesAny(["frontend", "repo"], DEEP_GROUPS), false);
  assert.equal(reachesAny([], DEEP_GROUPS), false);
});

const workflow = (name: string): unknown => parse(readFileSync(new URL(`../workflows/${name}`, import.meta.url), "utf8"), { merge: true });

// CodeQL skips the same documentation-only changes through paths-ignore.
test("codeql.yaml ignores exactly the inert paths", () => {
  const codeql = workflow("codeql.yaml") as { on: { push: { "paths-ignore": string[] }; pull_request: { "paths-ignore": string[] } } };
  assert.deepEqual(codeql.on.push["paths-ignore"], INERT);
  assert.deepEqual(codeql.on.pull_request["paths-ignore"], INERT);
});

test("ci.yaml reads the outputs this sets", () => {
  const ci = workflow("ci.yaml") as { jobs: { changes: { outputs: Record<string, string> } } };
  for (const output of Object.values(Output)) {
    assert.equal(ci.jobs.changes.outputs[output], `\${{ steps.filter.outputs.${output} }}`);
  }
});

test("every group is a verify task", () => {
  const taskfile = parse(readFileSync(new URL("../../Taskfile.dev.yaml", import.meta.url), "utf8")) as { tasks: Record<string, unknown> };
  for (const group of ALL_GROUPS) {
    assert.ok(`verify-${group}` in taskfile.tasks, group);
  }
});

describe("candidateRange", () => {
  test("a pull request's ends win", () => {
    assert.deepEqual(candidateRange({ base: "a", head: "b", before: "c", after: "d" }), { from: "a", to: "b" });
  });
  test("a push's otherwise", () => assert.deepEqual(candidateRange({ before: "c", after: "d" }), { from: "c", to: "d" }));
  test("a new branch, or no event, names no diff", () => {
    assert.equal(candidateRange({ before: "0".repeat(40), after: "d" }), undefined);
    assert.equal(candidateRange({}), undefined);
  });
});

describe("classify", () => {
  test("the diff's paths", async () => {
    const repo = await tempRepo();
    try {
      repo.write("README.md", "x");
      const from = await repo.commit("first");
      repo.write("frontend/a.ts", "x");
      const to = await repo.commit("second");
      assert.deepEqual(await classify({ before: from, after: to }, repo.dir), ["frontend", "repo"]);
    } finally {
      repo.cleanup();
    }
  });
  test("a base this clone lacks reaches everything", async () => {
    const repo = await tempRepo();
    try {
      const to = await repo.commit("only");
      assert.deepEqual(await classify({ before: "1".repeat(40), after: to }, repo.dir), ALL_GROUPS);
    } finally {
      repo.cleanup();
    }
  });
});

describe("changesSummary", () => {
  test("names every check, and whether this change runs it", () => {
    const summary = changesSummary(["backend", "repo"], true, false);
    assert.ok(summary.includes(`| Verify (backend) | ${RUNS} |`), summary);
    assert.ok(summary.includes(`| Verify (sdk) | ${SKIPPED} |`), summary);
    assert.ok(summary.includes(`| End-to-end | ${RUNS} |`), summary);
    assert.ok(summary.includes(`| Deep | ${SKIPPED} |`), summary);
    assert.equal(summary.includes("Documentation only"), false);
  });

  test("says a change of documentation alone runs nothing", () => {
    const summary = changesSummary([], false, false);
    assert.ok(summary.includes("Documentation only"), summary);
    assert.equal(summary.includes(`| ${RUNS} |`), false, summary);
  });

  // The rows name the jobs as ci.yaml names them.
  test("names the jobs ci.yaml has", () => {
    const names = Object.values((parse(readFileSync(new URL("../workflows/ci.yaml", import.meta.url), "utf8")) as { jobs: Record<string, { name?: string }> }).jobs).map((j) => j.name?.replace(/\s*#.*$/, ""));
    for (const job of ["Verify", "End-to-end", "Deep"]) assert.ok(names.includes(job), `ci.yaml has no ${job} job`);
  });
});
