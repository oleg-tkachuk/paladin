import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Vitest treats a function returned from beforeEach / beforeAll as that
// hook's teardown and calls it after the test. `beforeEach(() =>
// mock.mockReset())` returns the mock, so the mock ran once more after every
// test — and when it rejected, the test failed with an error the test never
// raised. Ten test files had the shape; one had already bitten.

const SRC = join(__dirname, "..");
const HOOKS = new Set(["beforeEach", "beforeAll", "afterEach", "afterAll"]);

function testFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "gen" ? [] : testFiles(p);
    return /\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/**
 * Hooks whose arrow callback returns a mock: `.mockReset()`, `.mockClear()`
 * and the rest return the mock itself, which is a function. (`vi.useFakeTimers()`
 * and friends return `vi`, which is not, and Vitest leaves it alone.)
 */
function returningHooks(): string[] {
  const found: string[] = [];
  for (const file of testFiles(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const visit = (node: ts.Node) => {
      if (
        ts.isCallExpression(node) &&
        ts.isIdentifier(node.expression) &&
        HOOKS.has(node.expression.text)
      ) {
        const fn = node.arguments[0];
        if (
          fn &&
          ts.isArrowFunction(fn) &&
          ts.isCallExpression(fn.body) &&
          ts.isPropertyAccessExpression(fn.body.expression) &&
          fn.body.expression.name.text.startsWith("mock")
        ) {
          const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line;
          found.push(`${relative(SRC, file)}:${line + 1}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("test hooks", () => {
  it("return nothing Vitest would run as a teardown", () => {
    expect(returningHooks(), "wrap the body in braces").toEqual([]);
  });
});
