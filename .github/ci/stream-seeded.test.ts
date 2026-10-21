import assert from "node:assert/strict";
import { test } from "node:test";

import { tempRepo } from "./lib/temp-repo.ts";
import { seeded } from "./stream-seeded.ts";

// A stream with no tag would start at 1.0.0; one that only looks like its
// tag, or another stream's tag, does not seed it.
test("a stream is seeded by a release tag of its own", async () => {
  const repo = await tempRepo();
  try {
    repo.write("a", "a");
    await repo.commit("feat: a");
    await repo.run("tag", "sdk/go/v0.102.0");
    await repo.run("tag", "sdk/python/vnext");
    assert.equal(await seeded("python-sdk", repo.dir), false);
    assert.equal(await seeded("go-sdk", repo.dir), true);
    await repo.run("tag", "sdk/python/v0.102.0");
    assert.equal(await seeded("python-sdk", repo.dir), true);
    await assert.rejects(seeded("ruby-sdk", repo.dir), /no ruby-sdk stream/);
  } finally {
    repo.cleanup();
  }
});
