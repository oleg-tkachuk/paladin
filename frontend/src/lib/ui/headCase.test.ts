import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A table whose sortable headers read in HEAD_LABEL's small capitals has its
// other headers read the same: a plain TableHead with words beside them reads
// as a different table. StaticHead is the header that does not sort.

const SRC = join(__dirname, "..", "..");
const SORTABLE = "SortableHead";
const PLAIN = "TableHead";

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
    return /\.tsx$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
  });
}

/** The plain headers with words in them, in a file that also sorts. */
function plainWordHeads(file: string): string[] {
  const text = readFileSync(file, "utf8");
  if (!text.includes(`<${SORTABLE}`)) return [];
  const sf = ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const out: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      ts.isJsxElement(node) &&
      node.openingElement.tagName.getText() === PLAIN &&
      node.children.some((c) => ts.isJsxText(c) && /[A-Za-z]/.test(c.getText()))
    ) {
      const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
      out.push(`${relative(SRC, file)}:${line + 1}`);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

describe("table headers", () => {
  it("read alike beside sortable ones: StaticHead, not a plain TableHead", () => {
    expect(sources(SRC).flatMap(plainWordHeads)).toEqual([]);
  });
});
