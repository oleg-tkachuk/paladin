import assert from "node:assert/strict";
import { test } from "node:test";

import { pullNumber, updatable, updateBehind, updatedSummary } from "./auto-merge.ts";
import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { MergeState, type OpenPullRequest } from "./lib/github.ts";

const pull = (number: number, over: Partial<OpenPullRequest> = {}): OpenPullRequest => ({
  number,
  draft: false,
  fromFork: false,
  mergeState: MergeState.Behind,
  ...over,
});

test("only a ready, same-repository pull request that is behind is updated", () => {
  assert.equal(updatable(pull(1)), true);
  assert.equal(updatable(pull(2, { draft: true })), false);
  assert.equal(updatable(pull(3, { fromFork: true })), false);
  assert.equal(updatable(pull(4, { mergeState: MergeState.Dirty })), false);
  assert.equal(updatable(pull(5, { mergeState: MergeState.Clean })), false);
});

test("one that cannot be updated does not stop the rest", async () => {
  const data = fakeData();
  data.open = [pull(1), pull(2), pull(3, { draft: true }), pull(4)];
  data.updateFails.add(2);
  assert.deepEqual(await updateBehind(fakeGitHub(data)), [1, 4]);
  assert.deepEqual(data.updated, [1, 4]);
});

test("a pull request number is a positive integer", () => {
  assert.equal(pullNumber("655"), 655);
  for (const bad of ["", "0", "-1", "6a", "1.5"]) {
    assert.throws(() => pullNumber(bad), /is not a number/, bad);
  }
});

test("updatedSummary links each pull request it updated", () => {
  assert.ok(updatedSummary([7, 9], "o/r").includes("- [#7](https://github.com/o/r/pull/7)\n- [#9](https://github.com/o/r/pull/9)"));
  assert.ok(updatedSummary([], "o/r").includes("None was behind."));
});
