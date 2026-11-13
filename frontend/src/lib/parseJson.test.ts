import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, it, expect } from "vitest";
import { z } from "zod/mini";

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
      aud: z.optional(z.union([z.string(), z.array(z.string())])),
    });
    expect(safeParseJson(Aud, '{"aud":{"$ne":null}}')).toBeNull();
    expect(safeParseJson(Aud, '{"aud":["data","admin"]}')).toEqual({
      aud: ["data", "admin"],
    });
  });
});

describe("zod in the browser", () => {
  // zod/mini is the same library without its chainable methods, so a
  // bundler drops what a schema does not call; the full "zod" ships whole,
  // about 50 KiB more on every page. config.ts is the exception: only
  // next.config.ts reads it, at build time.
  const SRC = join(__dirname, "..");
  const BUILD_TIME_ONLY = new Set(["config.ts"]);

  function sources(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
      const path = join(dir, e.name);
      if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
      return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
    });
  }

  it('imports "zod/mini", never the full "zod"', () => {
    const full = sources(SRC)
      .map((f) => relative(SRC, f))
      .filter((f) => !BUILD_TIME_ONLY.has(f))
      .filter((f) =>
        /from "zod(\/v4)?(\/classic)?"/.test(
          readFileSync(join(SRC, f), "utf8"),
        ),
      );
    expect(full).toEqual([]);
  });
});
