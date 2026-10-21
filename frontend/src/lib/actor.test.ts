import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { SUBJECT_PREFIX, parseActor } from "./actor";

// The table the server's prefixes are held to as well.
const SHARED = resolve(__dirname, "../../../sdk/testdata/actor_subjects.json");
const ID = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";

describe("parseActor", () => {
  it("spells the prefixes as the server does", () => {
    const table = JSON.parse(readFileSync(SHARED, "utf8")) as Record<
      string,
      string
    >;
    expect(SUBJECT_PREFIX).toEqual({
      apiToken: table.api_token,
      capability: table.capability,
      system: table.system,
    });
  });

  it.each([
    [ID, { kind: "user", id: ID }],
    [`apikey:${ID}`, { kind: "apiToken", id: ID }],
    [`capability:${ID}`, { kind: "capability", id: ID }],
    ["system:bootstrap", { kind: "other" }],
    ["apikey:", { kind: "other" }],
    ["apikey:not-an-id", { kind: "other" }],
    ["", { kind: "other" }],
  ])("%j", (subject, want) => {
    expect(parseActor(subject)).toEqual(want);
  });
});
