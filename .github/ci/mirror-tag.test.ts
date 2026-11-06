import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { git } from "./lib/git.ts";
import { tempRepo } from "./lib/temp-repo.ts";
import { mirrorTags } from "./mirror-tag.ts";

const PYTHON_TAG = "sdk/python/v0.139.0";
const GO_TAG = "sdk/go/v0.139.0";

// One sequence, as successive runs see it.
test("each SDK release on HEAD gets its Go module tag, once", async () => {
  const base = mkdtempSync(join(tmpdir(), "paladin-ci-"));
  const remote = join(base, "remote.git");
  const repo = await tempRepo();
  try {
    await git(["init", "--quiet", "--bare", remote]);
    await repo.commit("feat(sdk): one");
    assert.deepEqual(await mirrorTags(repo.dir, remote), [], "no SDK release on HEAD");

    await repo.run("tag", "v40.0.0");
    await repo.run("tag", "sdk/python/vnext");
    assert.deepEqual(await mirrorTags(repo.dir, remote), [], "neither the product's tag nor a tag that only looks like a release");

    const head = await repo.commit("feat(sdk): two");
    await repo.run("tag", PYTHON_TAG);
    assert.deepEqual(await mirrorTags(repo.dir, remote), [GO_TAG], "the release's Go module tag");
    assert.equal((await git(["rev-parse", `${GO_TAG}^{commit}`], remote)).trim(), head, "pushed, on the release's commit");

    assert.deepEqual(await mirrorTags(repo.dir, remote), [], "a re-run finds it in place");

    await repo.commit("feat(sdk): three");
    await repo.run("tag", "sdk/python/v0.140.0");
    await repo.run("tag", "sdk/go/v0.140.0", head);
    await assert.rejects(mirrorTags(repo.dir, remote), /sdk\/go\/v0\.140\.0 points at .*one version names two commits/);
  } finally {
    repo.cleanup();
    rmSync(base, { recursive: true, force: true });
  }
});
