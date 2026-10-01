import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Numbers and times are written through lib/format, in DISPLAY_LOCALE. When
// this was measured, 15 call sites formatted in the viewer's locale instead —
// counts on quotas and billing, timestamps on objects, versions, trash and the
// health page — so two operators comparing the same screen read different
// digits and a different half of the day.
//
// Matched on the AST: a call to toLocaleString / toLocaleDateString /
// toLocaleTimeString, or `new Intl.<Format>`, outside lib/format, whose first
// argument is not DISPLAY_LOCALE. Prefer the formatters in locale.ts; a
// component that needs its own Intl object (RelativeTime's
// RelativeTimeFormat) passes the constant.

const LOCALE_METHODS = new Set([
  "toLocaleString",
  "toLocaleDateString",
  "toLocaleTimeString",
]);
const FORMAT_DIR = "lib/format/";
const PINNED = "DISPLAY_LOCALE";

const pinned = (
  args: ts.NodeArray<ts.Expression> | undefined,
  sf: ts.SourceFile,
) => !!args && args.length > 0 && args[0].getText(sf) === PINNED;
const SRC = join(__dirname, "..", "..");

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function viewerLocaleFormatting(): string[] {
  const found: string[] = [];
  for (const file of sourceFiles(SRC)) {
    const rel = relative(SRC, file);
    if (rel.startsWith(FORMAT_DIR)) continue;
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const at = (n: ts.Node) =>
      `${rel}:${sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1}`;
    const visit = (node: ts.Node) => {
      if (
        ts.isCallExpression(node) &&
        ts.isPropertyAccessExpression(node.expression) &&
        LOCALE_METHODS.has(node.expression.name.text) &&
        !pinned(node.arguments, sf)
      ) {
        found.push(`${at(node)} ${node.expression.name.text}`);
      }
      if (
        ts.isNewExpression(node) &&
        ts.isPropertyAccessExpression(node.expression) &&
        node.expression.expression.getText(sf) === "Intl" &&
        !pinned(node.arguments, sf)
      ) {
        found.push(`${at(node)} new Intl.${node.expression.name.text}`);
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("display locale", () => {
  it("formats numbers and times only through lib/format", () => {
    expect(
      viewerLocaleFormatting(),
      "use formatCount / formatDateTime / formatTime from @/lib/format/locale",
    ).toEqual([]);
  });
});
