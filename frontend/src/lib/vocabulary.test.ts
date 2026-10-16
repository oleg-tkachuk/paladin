import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// One vocabulary for the console (ADR-0033). These are the exact strings it
// once showed for things that have another name, matched in what reaches the
// screen — JSX text and string literals — not in comments, and not as loose
// phrases: "object key" is also an S3 object's key, which is a fine thing to
// say about an object.
const RETIRED: Record<string, string> = {
  "Object Key": "a Collection is a Collection",
  "All Object Keys": "a Collection is a Collection",
  "object keys": "a Collection is a Collection",
  "Failed to load object key.": "a Collection is a Collection",
  "Default Route": "the default binding is the default bucket",
  "Default route": "the default binding is the default bucket",
  "Clear route": "the default binding is the default bucket",
  "S3 Buckets": "Buckets",
  "Storage (backend/bucket)": "the column is the tenant's default bucket",
};

const SRC = join(__dirname, "..");
const GENERATED = join(SRC, "gen");

function sourceFiles(dir: string): string[] {
  if (dir === GENERATED) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/** Every piece of text in a file that can reach the screen. */
function shownStrings(file: string): string[] {
  const text = readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
  const out: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isJsxText(node)) out.push(node.text.replace(/\s+/g, " ").trim());
    else if (
      ts.isStringLiteral(node) ||
      ts.isNoSubstitutionTemplateLiteral(node)
    )
      out.push(node.text);
    else if (ts.isTemplateExpression(node)) {
      out.push(
        node.head.text,
        ...node.templateSpans.map((s) => s.literal.text),
      );
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

describe("console vocabulary", () => {
  it("shows no retired name", () => {
    const found: string[] = [];
    for (const file of sourceFiles(SRC)) {
      for (const s of shownStrings(file)) {
        for (const retired of Object.keys(RETIRED)) {
          if (s.includes(retired)) {
            found.push(
              `${relative(SRC, file)}: "${retired}" — ${RETIRED[retired]}`,
            );
          }
        }
      }
    }
    expect(found).toEqual([]);
  });

  // The scan reads JSX text; a parser change that hid it would pass vacuously.
  it("reads the strings a page shows", () => {
    const page = join(SRC, "app", "buckets", "page.tsx");
    expect(shownStrings(page)).toContain("New bucket");
  });
});
