import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A table body that says, by hand, both that its read failed and that it has
// no rows is ListBody written again: the order of the states, which keeps a
// failed list from reading as an empty one, belongs to the template.

const SRC = join(__dirname, "..", "..", "..");
const TEMPLATES = __dirname;

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sources(path);
    return /\.tsx$/.test(e.name) && !/\.test\./.test(e.name) ? [path] : [];
  });
}

function tags(node: ts.Node, out = new Set<string>()): Set<string> {
  if (ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) {
    out.add(node.tagName.getText());
  }
  ts.forEachChild(node, (c) => void tags(c, out));
  return out;
}

function handRolledBodies(): string[] {
  const out: string[] = [];
  for (const file of sources(SRC)) {
    if (file.startsWith(TEMPLATES)) continue;
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );
    const visit = (node: ts.Node) => {
      if (
        ts.isJsxElement(node) &&
        node.openingElement.tagName.getText() === "TableBody"
      ) {
        const inside = tags(node);
        if (inside.has("EmptyTableRow") && inside.has("ListLoadError")) {
          const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
          out.push(`${relative(SRC, file)}:${line + 1}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return out;
}

describe("list states", () => {
  it("are laid out by ListBody, not by hand", () => {
    expect(handRolledBodies()).toEqual([]);
  });
});
