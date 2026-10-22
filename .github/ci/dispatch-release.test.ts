import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { parse } from "yaml";

import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { Payload, RELEASE_EVENT, dispatchRelease, dispatchSummary } from "./dispatch-release.ts";

const SHA = "622e006bda05880b265e4fb9b96c6441f49ce955";
const DEV_VERSION = "39.2.1-dev.1792562188.g622e006b";

test("dispatches the commit and the run that vouches for it", async () => {
  const data = fakeData();
  await dispatchRelease(fakeGitHub(data), SHA, "42");
  assert.deepEqual(data.dispatched, [
    { eventType: RELEASE_EVENT, payload: { sha: SHA, ci_run_id: "42", label: "622e006" } },
  ]);
});

// The label the release run is named after reads as the console's build label.
test("labels the dev build as the console does", async () => {
  const data = fakeData();
  await dispatchRelease(fakeGitHub(data), SHA, "42", DEV_VERSION);
  assert.equal(data.dispatched[0]?.payload[Payload.Label], "v39.2.1-dev (622e006)");
});

test("labels with the commit alone a version that is no dev build's", async () => {
  const data = fakeData();
  await dispatchRelease(fakeGitHub(data), SHA, "42", "39.2.1");
  assert.equal(data.dispatched[0]?.payload[Payload.Label], "622e006");
});

const workflow = (name: string): Record<string, unknown> =>
  parse(readFileSync(new URL(`../workflows/${name}`, import.meta.url), "utf8")) as Record<string, unknown>;

test("release.yaml runs on that event", () => {
  const release = workflow("release.yaml") as { on: { repository_dispatch: { types: string[] } } };
  assert.deepEqual(release.on.repository_dispatch.types, [RELEASE_EVENT]);
});

// The run's name says what it releases, and a dev build's carries the label
// this dispatch sends.
test("release.yaml names each run for what it releases", () => {
  const runName = String(workflow("release.yaml")["run-name"]);
  for (const kind of ["'DevRelease {0}'", "'PreRelease'", "'Release'"]) {
    assert.ok(runName.includes(kind), `run-name names no ${kind}`);
  }
  assert.ok(runName.includes(`client_payload.${Payload.Label}`), "a dev build's run is not named after its label");
  assert.ok(runName.includes(`client_payload.${Payload.Sha}`), "a dev build without a version is not named after its commit");
});

test("CI dispatches with the dev version it derived", () => {
  const ci = workflow("ci.yaml") as { jobs: Record<string, { steps: { id?: string; env?: Record<string, string> }[] }> };
  const steps = ci.jobs["dispatch-release"]?.steps ?? [];
  const derived = steps.find((s) => s.id !== undefined && s.id !== "");
  assert.ok(derived, "dispatch-release derives no version");
  const dispatch = steps.find((s) => s.env?.["VERSION"] !== undefined);
  assert.equal(dispatch?.env?.["VERSION"], `\${{ steps.${derived.id}.outputs.version }}`);
});

test("dispatchSummary names the release run it started", () => {
  assert.ok(dispatchSummary("v39.2.1-dev (622e006)").includes("`v39.2.1-dev (622e006)`"));
});
