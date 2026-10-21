import assert from "node:assert/strict";
import { test } from "node:test";

import { devBuildLabel } from "./versions.ts";

const SHA = "622e006bda05880b265e4fb9b96c6441f49ce955";

test("devBuildLabel reads as the console's build label", () => {
  for (const [version, want] of [
    ["39.2.1-dev.1792562188.g622e006b", "v39.2.1-dev (622e006)"],
    // A version without the time-and-commit suffix keeps what it has.
    ["39.2.1-dev.7", "v39.2.1-dev.7 (622e006)"],
    // No dev build's version: the commit alone.
    ["39.2.1", "622e006"],
    [undefined, "622e006"],
  ] as const) {
    assert.equal(devBuildLabel(SHA, version), want, String(version));
  }
});
