import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// One behaviour, one implementation. When this was measured, eight pages each
// carried their own byte-identical copy of the UTC timestamp formatter; a fix
// to one would have left seven showing the old output.
//
// A function body (whitespace collapsed) that appears in more than one file
// fails, unless every copy is listed below with the reason it is still there.
// Bodies shorter than MIN_BODY_CHARS are left alone: `reset(); onClose();`
// written twice is not one behaviour with two implementations.

const SRC = join(__dirname, "..");
const GENERATED = "gen";
const MIN_BODY_CHARS = 100;

// "<file under src>#<function>" → why the copy is still there. An entry that
// no longer names a copy fails, so the list only shrinks.
const ALLOWED: Record<string, string> = {
  "app/buckets/page.tsx#SortHeader": "sort headers: one shared component next",
  "app/tenants/[id]/buckets/page.tsx#SortHeader":
    "sort headers: one shared component next",
  "app/tenants/page.tsx#SortHeader": "sort headers: one shared component next",
  "app/collections/page.tsx#SortHeader":
    "sort headers: one shared component next",
  "app/tenants/[id]/collections/page.tsx#SortHeader":
    "sort headers: one shared component next",
  "app/buckets/page.tsx#handleDelete":
    "the platform and tenant bucket tables are still two components",
  "app/tenants/[id]/buckets/page.tsx#handleDelete":
    "the platform and tenant bucket tables are still two components",
  "app/buckets/page.tsx#ProvisionStateBadge":
    "the platform and tenant bucket tables are still two components",
  "app/tenants/[id]/buckets/page.tsx#ProvisionStateBadge":
    "the platform and tenant bucket tables are still two components",
  "app/storage-backends/[backendId]/BackendActions.tsx#errText":
    "transport's describe() is module-private; export it next",
  "lib/connect/transport.ts#describe":
    "transport's describe() is module-private; export it next",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/** Function bodies by text, each with the "file#name" of every copy. */
function bodies(): Map<string, string[]> {
  const byBody = new Map<string, string[]>();
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const visit = (node: ts.Node) => {
      let body: ts.Block | undefined;
      let name = "";
      if (ts.isFunctionDeclaration(node) && node.body) {
        body = node.body;
        name = node.name?.text ?? "default";
      } else if (
        ts.isVariableDeclaration(node) &&
        node.initializer &&
        (ts.isArrowFunction(node.initializer) ||
          ts.isFunctionExpression(node.initializer)) &&
        ts.isBlock(node.initializer.body)
      ) {
        body = node.initializer.body;
        name = node.name.getText(sf);
      }
      if (body) {
        const text = body.getText(sf).replace(/\s+/g, " ");
        if (text.length >= MIN_BODY_CHARS) {
          const at = `${relative(SRC, file)}#${name}`;
          byBody.set(text, [...(byBody.get(text) ?? []), at]);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return byBody;
}

const fileOf = (at: string) => at.split("#")[0];

describe("duplicate functions", () => {
  const copies = [...bodies().values()].filter(
    (at) => new Set(at.map(fileOf)).size > 1,
  );

  it("no behaviour is implemented twice outside the allowed list", () => {
    const unexplained = copies
      .filter((at) => !at.every((a) => a in ALLOWED))
      .map((at) => at.join(" = "));
    expect(
      unexplained,
      "import the one implementation instead of copying it",
    ).toEqual([]);
  });

  it("every allowed entry still names a copy", () => {
    const named = new Set(copies.flat());
    expect(Object.keys(ALLOWED).filter((a) => !named.has(a))).toEqual([]);
  });
});
