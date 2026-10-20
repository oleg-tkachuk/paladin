import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { OP_CHOICES } from "./_constants";

// The table of capability operations both SDKs and the server are held to.
const SHARED_OPS = resolve(
  __dirname,
  "../../../../../../sdk/testdata/capability_ops.json",
);

describe("capability op choices", () => {
  it("offer exactly limes's operations, then the data plane's own", () => {
    const table = JSON.parse(readFileSync(SHARED_OPS, "utf8")) as {
      limes: string[];
      data_plane: Record<string, string>;
    };
    expect(OP_CHOICES).toEqual([
      ...table.limes,
      ...Object.values(table.data_plane),
    ]);
  });
});
