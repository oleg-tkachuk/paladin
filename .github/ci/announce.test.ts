import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { announce } from "./announce.ts";
import { fakeData, fakeGitHub } from "./lib/fake-github.ts";

test("the release, with generated notes and every SBOM", async () => {
  const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
  try {
    const data = fakeData();
    await assert.rejects(announce(fakeGitHub(data), "v1.0.0", dir), /no SBOM/);
    for (const f of ["b.spdx.json", "a.spdx.json", "notes.txt"]) writeFileSync(join(dir, f), "{}");
    await announce(fakeGitHub(data), "v1.0.0", dir);
    assert.deepEqual(data.published, [
      { tag: "v1.0.0", title: "v1.0.0", notes: { generated: true }, latest: true, assets: [join(dir, "a.spdx.json"), join(dir, "b.spdx.json")] },
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
