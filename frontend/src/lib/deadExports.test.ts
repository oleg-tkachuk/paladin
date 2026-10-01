import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Code that nothing runs. Measured before the cleanup: an EmptyState
// component, two Connect clients, formatUptime, a separator constant, a type
// and an audience list — each exported, some tested, none imported by the
// console. A test of dead code keeps it looking alive.
//
// An export fails when no other non-test file under src (or next.config.ts)
// names it. Matching is by name, so a common word can hide a dead export; it
// cannot flag a live one.

const ROOT = join(__dirname, "..", "..");
const SRC = join(ROOT, "src");
const GENERATED = "gen";
// Files Next.js loads by convention: their exports are read by the framework.
const NEXT_CONVENTION =
  /(^|\/)(page|layout|route|error|not-found|loading|template|default|global-error)\.tsx?$|(^|\/)(middleware|proxy|instrumentation)\.ts$/;
const OUTSIDE_SRC_READERS = [join(ROOT, "next.config.ts")];

// "<file under src>#<export>" → why it stays without a production reader.
// An entry that no longer names an unread export fails.
const ALLOWED: Record<string, string> = {
  "lib/auth/tokenStore.ts#peekAccessToken":
    "test seam: the token cache is module-private",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) ? [p] : [];
  });
}

const isTest = (f: string) => /\.test\.tsx?$/.test(f) || f.includes("/test/");

function exportedNames(sf: ts.SourceFile): string[] {
  return sf.statements.flatMap((st) => {
    const exported = ts.canHaveModifiers(st)
      ? ts.getModifiers(st)?.some((m) => m.kind === ts.SyntaxKind.ExportKeyword)
      : false;
    if (!exported) return [];
    if (ts.isVariableStatement(st)) {
      return st.declarationList.declarations.flatMap((d) =>
        ts.isIdentifier(d.name) ? [d.name.text] : [],
      );
    }
    const name = (st as { name?: ts.Identifier }).name;
    return name ? [name.text] : [];
  });
}

function unread(): string[] {
  const files = sources(SRC);
  const readers = [...files.filter((f) => !isTest(f)), ...OUTSIDE_SRC_READERS];
  const text = new Map(readers.map((f) => [f, readFileSync(f, "utf8")]));
  const found: string[] = [];
  for (const file of files) {
    const rel = relative(SRC, file);
    if (isTest(file) || NEXT_CONVENTION.test(rel)) continue;
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    for (const name of exportedNames(sf)) {
      const word = new RegExp(`\\b${name}\\b`);
      const own = readFileSync(file, "utf8").match(
        new RegExp(`\\b${name}\\b`, "g"),
      );
      const readElsewhere = readers.some(
        (r) => r !== file && word.test(text.get(r) ?? ""),
      );
      // Used in its own file is still used; only the export is surplus.
      if (!readElsewhere && (own?.length ?? 0) <= 1) {
        found.push(`${rel}#${name}`);
      }
    }
  }
  return found;
}

describe("dead exports", () => {
  const found = unread();

  it("every export is read by the console", () => {
    expect(
      found.filter((k) => !(k in ALLOWED)),
      "delete it, and its test",
    ).toEqual([]);
  });

  it("every allowed entry still names an unread export", () => {
    expect(Object.keys(ALLOWED).filter((k) => !found.includes(k))).toEqual([]);
  });
});
