import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A useQuery whose error nothing reads renders its failure as whatever the
// empty data looks like. Measured when this landed: five pages toasted a
// failed list and then showed "No M2M tokens yet", "No capabilities for this
// principal", "No event subscriptions", "No historical versions yet" or an
// empty quota card — the toast gone in seconds, the empty state staying.
//
// Every useQuery / useInfiniteQuery result must have its `error` or `isError`
// read in the same file, unless listed here with what a failure looks like
// and why that is honest. The hook-level contract is gated separately
// (hooks/queryErrorContract.test.ts).

const SRC = join(__dirname, "..");
const GENERATED = "gen";
const QUERY_HOOKS = new Set(["useQuery", "useInfiniteQuery"]);
const ERROR_FIELDS = new Set(["error", "isError"]);

// "<file under src>#<variable>" → what a failure renders, and why it is honest.
const ALLOWED: Record<string, string> = {
  "app/trash/page.tsx#trashQuery":
    "its queryFn is useTenants().fetchTenants, whose error the page renders",
  "components/features/audit/ActorName.tsx#user":
    "a failed lookup shows the subject id, which is what it resolves",
  "components/ThemeSync.tsx#{data}":
    "a failed read keeps the current theme; /profile renders the error",
  "components/features/tenants/RenamedSlugHint.tsx#{data}":
    "an optional hint: a failed lookup shows no hint, and nothing else",
  "context/ScopeContext.tsx#tenantQuery":
    "the scope shows the tenant id it already has, and logs the failure",
  "hooks/useDistinctTags.ts#{data}":
    "tag filter options; the object list reports its own failed read",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function unreadQueryErrors(): string[] {
  const found: string[] = [];
  for (const file of sources(SRC)) {
    const text = readFileSync(file, "utf8");
    if (!/use(Infinite)?Query\(/.test(text)) continue;
    const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
    const rel = relative(SRC, file);
    const visit = (node: ts.Node) => {
      if (
        ts.isVariableDeclaration(node) &&
        node.initializer &&
        ts.isCallExpression(node.initializer) &&
        ts.isIdentifier(node.initializer.expression) &&
        QUERY_HOOKS.has(node.initializer.expression.text)
      ) {
        if (ts.isIdentifier(node.name)) {
          const v = node.name.text;
          const read = new RegExp(`\\b${v}\\??\\.(error|isError)\\b`);
          // Returned whole from a hook: the caller reads it.
          const returned = new RegExp(`return [^;]*\\b${v}\\b|\\b${v}\\s*[,}]`);
          if (!read.test(text) && !returned.test(text)) {
            found.push(`${rel}#${v}`);
          }
        } else if (ts.isObjectBindingPattern(node.name)) {
          const names = node.name.elements.map((e) =>
            (e.propertyName ?? e.name).getText(sf),
          );
          if (!names.some((n) => ERROR_FIELDS.has(n))) {
            found.push(`${rel}#{${names.join(", ")}}`);
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("query errors", () => {
  const found = unreadQueryErrors();

  it("are read wherever a query is", () => {
    expect(
      found.filter((k) => !(k in ALLOWED)),
      "render the failure (ListLoadError), or list it here with why not",
    ).toEqual([]);
  });

  it("every allowed entry still names an unread query", () => {
    expect(Object.keys(ALLOWED).filter((k) => !found.includes(k))).toEqual([]);
  });
});
