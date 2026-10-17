import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A chip reads as part of its row: the badge's own text size and case, and
// its variant's colour. Badges were handed an upper-cased, wide-tracked, muted
// label class, so a chip read larger than the row beside it and lost its
// colour; a few set their own smaller size. Nothing but this keeps the next
// one from doing it again.

const SRC = join(__dirname, "..");
// What a Badge's className must not decide: its size, its case, its tracking.
const OVERRIDE =
  /\b(text-(xs|sm|base|lg|tiny|micro|\[[^\]]*\])|uppercase|lowercase|capitalize|tracking-\w+)\b/;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : sourceFiles(p);
    return /\.tsx$/.test(e.name) && !/\.test\.tsx$/.test(e.name) ? [p] : [];
  });
}

/** Every <Badge> whose className decides its size, case or tracking. */
function overridingBadges(): string[] {
  const found: string[] = [];
  for (const file of sourceFiles(SRC)) {
    const text = readFileSync(file, "utf8");
    if (!text.includes("<Badge")) continue;
    const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
    const visit = (node: ts.Node) => {
      if (
        (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) &&
        node.tagName.getText(sf) === "Badge"
      ) {
        for (const attr of node.attributes.properties) {
          if (
            ts.isJsxAttribute(attr) &&
            attr.name.getText(sf) === "className" &&
            attr.initializer &&
            OVERRIDE.test(attr.initializer.getText(sf))
          ) {
            const { line } = sf.getLineAndCharacterOfPosition(
              node.getStart(sf),
            );
            found.push(`${relative(SRC, file)}:${line + 1}`);
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("badge type", () => {
  it("leaves every badge's size, case and tracking to the badge", () => {
    expect(overridingBadges()).toEqual([]);
  });
});
