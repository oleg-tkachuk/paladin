import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { parse } from "yaml";

import { CI_DISPATCH, Kind, NOTHING, Output, plan, planSummary } from "./publish-plan.ts";

test("a release at its tag, a dev build of the tested commit", () => {
  assert.deepEqual(plan("v1.2.0", CI_DISPATCH, "abc123"), { kind: Kind.Release, ref: "v1.2.0" });
  assert.deepEqual(plan("v1.2.0", "workflow_dispatch", undefined), { kind: Kind.Release, ref: "v1.2.0" });
  assert.deepEqual(plan(undefined, CI_DISPATCH, "abc123"), { kind: Kind.Dev, ref: "abc123" });
  assert.equal(plan(undefined, "workflow_dispatch", "abc123"), undefined, "a manual run with no tag publishes nothing");
  assert.equal(plan(undefined, CI_DISPATCH, undefined), undefined, "a dispatch naming no commit publishes nothing");
});

test("release.yaml hands the plan to the publish job", () => {
  const workflow = parse(readFileSync(new URL("../workflows/release.yaml", import.meta.url), "utf8")) as {
    on: Record<string, unknown>;
    jobs: { release: { outputs: Record<string, string> } };
  };
  assert.ok(CI_DISPATCH in workflow.on);
  assert.equal(workflow.jobs.release.outputs.publish_kind, `\${{ steps.plan.outputs.${Output.Kind} }}`);
  assert.equal(workflow.jobs.release.outputs.publish_ref, `\${{ steps.plan.outputs.${Output.Ref} }}`);
});

test("planSummary says what the run publishes", () => {
  assert.ok(planSummary({ kind: Kind.Release, ref: "v40.0.0" }).includes("The release of `v40.0.0`"));
  assert.ok(planSummary({ kind: Kind.Dev, ref: "abc123" }).includes("A dev build of `abc123`"));
  assert.ok(planSummary(undefined).includes(NOTHING));
});
