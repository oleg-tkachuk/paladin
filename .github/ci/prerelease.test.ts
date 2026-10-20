import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { parse } from "yaml";

import { git } from "./lib/git.ts";
import { tempRepo } from "./lib/temp-repo.ts";
import { isCandidate } from "./lib/versions.ts";
import { candidateTag, tagCandidate } from "./prerelease.ts";

describe("candidateTag", () => {
  test("the first candidate of a version is rc.1", () => {
    assert.equal(candidateTag("v", "38.3.0", ["v38.2.1", "sdk/go/v0.9.0"]), "v38.3.0-rc.1");
  });
  test("one past the highest already tagged, by number", () => {
    assert.equal(candidateTag("v", "38.3.0", ["v38.3.0-rc.1", "v38.3.0-rc.9", "v38.3.0-rc.10"]), "v38.3.0-rc.11");
  });
  test("another version's candidates, or a malformed one, do not count", () => {
    assert.equal(candidateTag("v", "38.3.0", ["v38.2.0-rc.4", "v38.3.0-rc.x", "v38.3.0-rc.2.1"]), "v38.3.0-rc.1");
  });
  test("a candidate reads as one", () => {
    assert.ok(isCandidate("v38.3.0-rc.1"));
    assert.ok(isCandidate("38.3.0-rc.12"));
    for (const t of ["v38.3.0", "38.3.1-dev.1792.gabc", "v38.3.0-rc"]) assert.ok(!isCandidate(t), t);
  });
});

describe("tagCandidate", () => {
  test("tags HEAD and pushes the tag, or does nothing when no release is due", async () => {
    const base = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    const remote = join(base, "remote.git");
    const repo = await tempRepo();
    try {
      await git(["init", "--quiet", "--bare", remote]);
      const head = await repo.commit("feat(api): a thing");
      await repo.run("tag", "v38.3.0-rc.1");

      assert.equal(await tagCandidate(repo.dir, async () => undefined, remote), undefined);
      assert.equal((await git(["tag", "--list"], remote)).trim(), "");

      assert.equal(await tagCandidate(repo.dir, async () => "38.3.0", remote), "v38.3.0-rc.2");
      assert.equal((await git(["rev-parse", "v38.3.0-rc.2^{commit}"], remote)).trim(), head);
    } finally {
      repo.cleanup();
      rmSync(base, { recursive: true, force: true });
    }
  });
});

// The channel is chosen by hand; each step runs on its own channel only.
test("release.yaml offers both channels by hand and runs each one's step", () => {
  const workflow = parse(readFileSync(new URL("../workflows/release.yaml", import.meta.url), "utf8")) as {
    on: { workflow_dispatch: { inputs: { channel: { options: string[]; default: string } } } };
    jobs: { release: { steps: { name?: string; if?: string; run?: string }[] } };
  };
  const channel = workflow.on.workflow_dispatch.inputs.channel;
  assert.deepEqual(channel.options, ["release", "prerelease"]);
  assert.equal(channel.default, "release");
  const steps = workflow.jobs.release.steps;
  const tagStep = steps.find((s) => s.run?.includes("semantic-release") && s.if?.includes("workflow_dispatch"));
  const rcStep = steps.find((s) => s.run?.includes("prerelease.ts"));
  assert.match(tagStep?.if ?? "", /inputs\.channel == 'release'/);
  assert.match(rcStep?.if ?? "", /inputs\.channel == 'prerelease'/);
});
