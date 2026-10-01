import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// A TanStack hook returns a new result object on every render. A useCallback,
// useMemo or useEffect that lists that object as a dependency therefore runs
// or changes identity on every render. On the objects page that made refresh a
// new function each time; the page re-registered its palette actions on every
// change, which rendered it again — an endless loop. Depend on the stable
// members (refetch, fetchNextPage) or the values read from it instead.

const SRC = join(__dirname, "..");
const GENERATED = "gen";
const QUERY_HOOKS = new Set([
  "useQuery",
  "useInfiniteQuery",
  "useMutation",
  "useSuspenseQuery",
]);
const DEP_HOOKS = new Set([
  "useCallback",
  "useMemo",
  "useEffect",
  "useLayoutEffect",
]);

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

const calleeName = (call: ts.CallExpression): string =>
  ts.isIdentifier(call.expression) ? call.expression.text : "";

function findings(): { found: string[]; queryResults: number } {
  const found: string[] = [];
  let queryResults = 0;
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const results = new Set<string>();
    const collect = (n: ts.Node) => {
      if (
        ts.isVariableDeclaration(n) &&
        ts.isIdentifier(n.name) &&
        n.initializer &&
        ts.isCallExpression(n.initializer) &&
        QUERY_HOOKS.has(calleeName(n.initializer))
      ) {
        results.add(n.name.text);
      }
      ts.forEachChild(n, collect);
    };
    collect(sf);
    queryResults += results.size;
    const check = (n: ts.Node) => {
      if (ts.isCallExpression(n) && DEP_HOOKS.has(calleeName(n))) {
        const deps = n.arguments[1];
        if (deps && ts.isArrayLiteralExpression(deps)) {
          for (const d of deps.elements) {
            if (ts.isIdentifier(d) && results.has(d.text)) {
              const line =
                sf.getLineAndCharacterOfPosition(d.getStart(sf)).line + 1;
              found.push(`${relative(SRC, file)}:${line} depends on ${d.text}`);
            }
          }
        }
      }
      ts.forEachChild(n, check);
    };
    check(sf);
  }
  return { found, queryResults };
}

describe("TanStack results as dependencies", () => {
  const { found, queryResults } = findings();

  it("finds the query results it is meant to check", () => {
    expect(queryResults).toBeGreaterThan(10);
  });

  it("no hook depends on a whole query result", () => {
    expect(found, "depend on refetch / fetchNextPage / the value read").toEqual(
      [],
    );
  });
});
