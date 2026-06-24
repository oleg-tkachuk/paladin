import { describe, it, expect } from "vitest";
import { z } from "zod";

import { safeParseJson } from "./parseJson";

const Schema = z.object({
  name: z.string(),
  count: z.number(),
});

describe("safeParseJson", () => {
  it("returns the typed value for valid JSON matching the schema", () => {
    const out = safeParseJson(Schema, '{"name":"a","count":2}');
    expect(out).toEqual({ name: "a", count: 2 });
  });

  it("returns null for null / undefined / empty input", () => {
    expect(safeParseJson(Schema, null)).toBeNull();
    expect(safeParseJson(Schema, undefined)).toBeNull();
    expect(safeParseJson(Schema, "")).toBeNull();
  });

  it("returns null for malformed JSON instead of throwing", () => {
    expect(safeParseJson(Schema, "{not json")).toBeNull();
  });

  it("returns null when valid JSON fails the schema (wrong shape)", () => {
    // `count` is a string, not a number — would have slipped past an `as`.
    expect(safeParseJson(Schema, '{"name":"a","count":"2"}')).toBeNull();
    // missing field
    expect(safeParseJson(Schema, '{"name":"a"}')).toBeNull();
  });

  it("rejects a hostile array/object where a primitive was expected", () => {
    const Aud = z.object({
      aud: z.union([z.string(), z.array(z.string())]).optional(),
    });
    expect(safeParseJson(Aud, '{"aud":{"$ne":null}}')).toBeNull();
    expect(safeParseJson(Aud, '{"aud":["data","admin"]}')).toEqual({
      aud: ["data", "admin"],
    });
  });
});
