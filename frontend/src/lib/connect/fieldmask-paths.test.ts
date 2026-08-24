/**
 * FieldMask paths must be JSON field names — camelCase.
 *
 * google.protobuf.FieldMask serialises to a comma-separated list of the
 * message's JSON field names, so "max_object_count" is not a spelling variant
 * of "maxObjectCount": protojson rejects it as an invalid path and fails the
 * WHOLE request, mask and all. The failure is invisible until something
 * actually reads the response — the console showed a generic "Update failed"
 * while the quota it was editing never moved.
 *
 * That is easy to reintroduce, because the .proto spells these fields in
 * snake_case and copying from it feels right. Four call sites had it at once,
 * and a note in dev-bootstrap.sh had already documented the trap without
 * anything enforcing it. This is the enforcement.
 */
import { describe, it, expect } from "vitest";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const SRC = join(process.cwd(), "src");

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) {
      if (entry === "gen" || entry === "node_modules") continue;
      walk(p, out);
    } else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(p);
    }
  }
  return out;
}

/** Every string literal pushed into, or listed as, a FieldMask path. Matches
 *  both `paths: ["a", "b"]` and `paths.push("a")`. */
function maskPaths(source: string): string[] {
  const found: string[] = [];

  for (const m of source.matchAll(/paths:\s*\[([^\]]*)\]/g)) {
    for (const lit of m[1].matchAll(/"([^"]+)"/g)) found.push(lit[1]);
  }
  for (const m of source.matchAll(/paths\.push\(\s*"([^"]+)"\s*\)/g)) {
    found.push(m[1]);
  }
  return found;
}

describe("FieldMask paths", () => {
  it("are camelCase everywhere they are written", () => {
    const offenders: string[] = [];

    for (const file of walk(SRC)) {
      const source = readFileSync(file, "utf8");
      if (!source.includes("FieldMask")) continue;
      for (const path of maskPaths(source)) {
        if (path.includes("_")) {
          offenders.push(`${file.replace(SRC, "src")}: "${path}"`);
        }
      }
    }

    expect(
      offenders,
      "snake_case FieldMask paths are rejected by protojson " +
        '("contains invalid path") and fail the entire request:\n' +
        offenders.join("\n"),
    ).toEqual([]);
  });

  it("detects the shape it is meant to detect", () => {
    // Guards the matcher itself: a test that silently matched nothing would
    // pass forever while the defect walked back in.
    expect(maskPaths('paths: ["max_object_count", "disabled"]')).toEqual([
      "max_object_count",
      "disabled",
    ]);
    expect(maskPaths('paths.push("display_name");')).toEqual(["display_name"]);
    expect(maskPaths("nothing here")).toEqual([]);
  });
});
