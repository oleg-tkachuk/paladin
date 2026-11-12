import assert from "node:assert/strict";
import { test } from "node:test";

import { Outcome, mergeableNow, pullNumber, queue, updatable, updateBehind, updatedSummary } from "./auto-merge.ts";
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

test("a pull request still waiting on checks is queued to merge", async () => {
  const data = fakeData();
  assert.equal(await queue(fakeGitHub(data), 7), Outcome.Queued);
  assert.deepEqual(data.autoMerged, [7]);
  assert.deepEqual(data.merged, []);
});

// GitHub refuses to queue one that can merge already — a draft marked ready
// after its checks went green — so it is merged at the head that was read.
test("one GitHub will not queue because it can merge now is merged at its head", async () => {
  for (const mergeState of [MergeState.Clean, MergeState.Unstable, MergeState.HasHooks]) {
    const data = fakeData();
    data.autoMergeFails.add(7);
    data.pulls.set(7, { head: "abc123", mergeState });
    assert.equal(await queue(fakeGitHub(data), 7), Outcome.Merged, mergeState);
    assert.deepEqual(data.merged, [{ number: 7, head: "abc123" }], mergeState);
  }
});

test("a refusal for any other reason is not merged around", async () => {
  for (const mergeState of [MergeState.Blocked, MergeState.Behind, MergeState.Dirty, "UNKNOWN"]) {
    const data = fakeData();
    data.autoMergeFails.add(7);
    data.pulls.set(7, { head: "abc123", mergeState });
    await assert.rejects(queue(fakeGitHub(data), 7), /cannot be queued/, mergeState);
    assert.deepEqual(data.merged, [], mergeState);
  }
});

test("mergeableNow is the states GitHub would merge in without waiting", () => {
  assert.deepEqual(
    Object.values(MergeState).filter(mergeableNow),
    [MergeState.Clean, MergeState.Unstable, MergeState.HasHooks],
  );
});
