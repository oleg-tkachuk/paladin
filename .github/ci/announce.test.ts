import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { announce, announceSummary } from "./announce.ts";
import { fakeData, fakeGitHub } from "./lib/fake-github.ts";

test("the release, with generated notes and every SBOM", async () => {
  const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
  try {
    const data = fakeData();
    await assert.rejects(announce(fakeGitHub(data), "v1.0.0", dir), /no SBOM/);
    for (const f of ["b.spdx.json", "a.spdx.json", "notes.txt"]) writeFileSync(join(dir, f), "{}");
    await announce(fakeGitHub(data), "v1.0.0", dir);
    assert.deepEqual(data.published, [
      { tag: "v1.0.0", title: "v1.0.0", notes: { generated: true }, latest: true, prerelease: false, assets: [join(dir, "a.spdx.json"), join(dir, "b.spdx.json")] },
    ]);

    // A release candidate is a prerelease, and never the Latest.
    await announce(fakeGitHub(data), "v1.1.0-rc.1", dir);
    assert.deepEqual(
      data.published.slice(1).map((r) => [r.tag, r.latest, r.prerelease]),
      [["v1.1.0-rc.1", false, true]],
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("announceSummary links the release, and says a candidate is one", () => {
  const release = announceSummary("v40.0.0", "o/r", 6);
  assert.ok(release.includes("[v40.0.0](https://github.com/o/r/releases/tag/v40.0.0), the latest release, with 6 SBOMs."), release);
  assert.ok(announceSummary("v40.0.0-rc.1", "o/r", 6).includes("a release candidate"));
});
