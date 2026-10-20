import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { tempRepo } from "./lib/temp-repo.ts";
import { CI_WORKFLOW, MANUAL_EVENT, RELEASE_BRANCH, ciPassed, isHead, releasable } from "./release-head.ts";

describe("isHead: a dispatch releases only the commit CI passed", () => {
  test("the head, by its full or short sha; not once main moved past it", async () => {
    const repo = await tempRepo();
    try {
      const passed = await repo.commit("one");
      assert.equal(await isHead(passed, repo.dir), true);
      assert.equal(await isHead(passed.slice(0, 8), repo.dir), true);
      await repo.commit("two");
      assert.equal(await isHead(passed, repo.dir), false);
      assert.equal(await isHead("0".repeat(40), repo.dir), false);
      await assert.rejects(isHead("", repo.dir), /no dispatched sha/);
    } finally {
      repo.cleanup();
    }
  });
});

describe("ciPassed: a manual run releases a commit only once CI on main passed", () => {
  const cases: [string, string[], boolean][] = [
    ["a run that passed", ["success"], true],
    ["a re-run that passed after a failure", ["failure", "success"], true],
    ["a run that failed", ["failure"], false],
    ["a run still in progress", [""], false],
    ["no run at all", [], false],
    ["a skipped run is not a pass", ["skipped"], false],
  ];
  for (const [name, conclusions, want] of cases) {
    test(name, async () => {
      const data = fakeData();
      data.conclusions.abc123 = conclusions;
      assert.equal(await ciPassed(fakeGitHub(data), "abc123"), want);
    });
  }
  test("asks about push runs of ci.yaml on main for that commit", async () => {
    const data = fakeData();
    await ciPassed(fakeGitHub(data), "abc123");
    assert.deepEqual(data.queries, [`${CI_WORKFLOW} ${RELEASE_BRANCH} abc123`]);
  });
  test("an empty sha is refused", async () => {
    await assert.rejects(ciPassed(fakeGitHub(fakeData()), ""), /no sha/);
  });
});

test("a manual run asks CI about the head, a dispatch about its sha", async () => {
  const repo = await tempRepo();
  try {
    const head = await repo.commit("one");
    const data = fakeData();
    data.conclusions[head] = ["success"];
    assert.equal(await releasable(MANUAL_EVENT, "", () => fakeGitHub(data), repo.dir), true);
    assert.equal(await releasable("repository_dispatch", head, () => assert.fail("a dispatch asks no API"), repo.dir), true);
  } finally {
    repo.cleanup();
  }
});
