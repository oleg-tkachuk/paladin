import { describe, expect, it } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { holdLapsed } from "./reservations";

const NOW = new Date("2026-10-31T12:00:00Z");
const at = (iso: string) => timestampFromDate(new Date(iso));

describe("holdLapsed", () => {
  it.each([
    ["an hour ahead", at("2026-10-31T13:00:00Z"), false],
    ["at the instant", at("2026-10-31T12:00:00Z"), true],
    ["a second past", at("2026-10-31T11:59:59Z"), true],
    ["no expiry on the wire", undefined, false],
  ])("%s", (_, expiresAt, want) => {
    expect(holdLapsed(expiresAt, NOW)).toBe(want);
  });
});
