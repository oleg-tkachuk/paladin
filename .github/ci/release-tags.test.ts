import assert from "node:assert/strict";
import { test } from "node:test";

import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { tempRepo } from "./lib/temp-repo.ts";
import { productTag, streamTags, tagsOnHead } from "./release-tags.ts";

// One sequence, as the release sees it: each step tags HEAD further.
test("each run reports the tags it pushed, or the ones left unpublished", async () => {
  const repo = await tempRepo();
  try {
    await repo.commit("one");
    const data = fakeData();
    const github = fakeGitHub(data);
    let before: string[] = [];
    const product = () => productTag(github, before, repo.dir);
    const sdk = () => streamTags(github, before, repo.dir);

    assert.equal(await product(), undefined, "no tag on HEAD");

    await repo.run("tag", "api/v3.0.0");
    assert.equal(await product(), undefined, "a proto baseline is not a release");

    await repo.run("tag", "v4.2.0");
    assert.equal(await product(), "v4.2.0", "the tag this run pushed");

    before = await tagsOnHead(repo.dir);
    assert.equal(await product(), "v4.2.0", "a tag an earlier run pushed and never published");

    data.released.add("v4.2.0");
    assert.equal(await product(), undefined, "a tag an earlier run pushed and published");

    await repo.run("tag", "v4.2.1");
    assert.equal(await product(), "v4.2.1", "a new tag beside an old one");
    data.released.add("v4.2.1");

    await repo.run("tag", "sdk/go/v0.3.0");
    // The capability module released here as capability/v* before it moved to
    // its own repository; those tags are history, never a stream this run cut.
    await repo.run("tag", "capability/v0.2.0");
    // A tag that only starts like a stream's is no release of it.
    await repo.run("tag", "vnext");
    await repo.run("tag", "sdk/go/vlatest");
    assert.equal(await product(), "v4.2.1", "a module tag is not the product's");
    assert.deepEqual(await sdk(), ["sdk/go/v0.3.0"], "every new module tag, and no product tag");

    before = await tagsOnHead(repo.dir);
    assert.deepEqual(await sdk(), ["sdk/go/v0.3.0"], "a module tag an earlier run pushed and never published");

    data.released.add("sdk/go/v0.3.0");
    assert.deepEqual(await sdk(), [], "module tags an earlier run pushed and published");
  } finally {
    repo.cleanup();
  }
});
