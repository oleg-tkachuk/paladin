import assert from "node:assert/strict";
import { test } from "node:test";

import { devBuildLabel, isDevVersion } from "./versions.ts";

const SHA = "622e006bda05880b265e4fb9b96c6441f49ce955";

test("devBuildLabel reads as the console's build label", () => {
  for (const [version, want] of [
    ["39.2.1-dev.1792562188.g622e006b", "v39.2.1-dev (622e006)"],
    // After a release candidate.
    ["39.3.0-rc.1.dev.1792562188.g622e006b", "v39.3.0-rc.1.dev (622e006)"],
    // A version without the time-and-commit suffix keeps what it has.
    ["39.2.1-dev.7", "v39.2.1-dev.7 (622e006)"],
    // No dev build's version: the commit alone.
    ["39.2.1", "622e006"],
    [undefined, "622e006"],
  ] as const) {
    assert.equal(devBuildLabel(SHA, version), want, String(version));
  }
});

// After a release the library derives <next patch>-dev.<…>; after a release
// candidate, <x.y.z>-rc.<n>.dev.<…>, which sorts after the candidate and
// before its release. Both are dev builds; neither the candidate nor a
// release is.
test("isDevVersion knows a dev build after a release and after a candidate", () => {
  for (const [version, want] of [
    ["39.2.1-dev.1792562188.g622e006b", true],
    ["39.3.0-rc.1.dev.1792562188.g622e006b", true],
    ["39.3.0-rc.1.dev.1792562188.dirty.1792562999.g622e006b", true],
    ["39.3.0-rc.1", false],
    ["39.3.0", false],
  ] as const) {
    assert.equal(isDevVersion(version), want, version);
  }
});
