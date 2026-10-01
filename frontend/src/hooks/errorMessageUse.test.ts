import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// errorMessage() is the console's one way to read a message off a thrown
// value. When this was measured, 74 call sites unwrapped ConnectError by hand
// instead, in six shapes: some fell back to the Error's message, some to a
// fixed "X failed" that hid it, some to String(err). A change to how messages
// are read — trimming a prefix, say — would have reached seven files.
//
// Reading `.rawMessage` fails outside the files listed here.

const SRC = join(__dirname, "..");
const GENERATED = "gen";

// File under src → why it reads rawMessage itself.
const ALLOWED: Record<string, string> = {
  "hooks/errorContract.ts": "errorMessage() itself",
  "lib/connect/error.ts": "normalizeError(), the query-hook counterpart",
  "lib/connect/transport.ts":
    "below the hooks layer; logs the auth-mint failure",
  "lib/auth/loginFailure.ts":
    "server side: maps the IAM error onto the login route's HTTP status",
  "app/api/shell/route.ts": "server side: prefixes the Connect code",
  "context/ScopeContext.tsx": "a console.warn, not a message shown to anyone",
  "hooks/useCELValidation.ts":
    "falls back to the coded message when the server sent no detail",
  "hooks/useCedarValidation.ts":
    "falls back to the coded message when the server sent no detail",
};

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === GENERATED ? [] : sources(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function rawMessageReads(): Map<string, string[]> {
  const byFile = new Map<string, string[]>();
  for (const file of sources(SRC)) {
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const rel = relative(SRC, file);
    const visit = (node: ts.Node) => {
      if (
        ts.isPropertyAccessExpression(node) &&
        node.name.text === "rawMessage"
      ) {
        const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line;
        byFile.set(rel, [...(byFile.get(rel) ?? []), `${rel}:${line + 1}`]);
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return byFile;
}

describe("error messages", () => {
  const reads = rawMessageReads();

  it("are read through errorMessage()", () => {
    const stray = [...reads]
      .filter(([file]) => !(file in ALLOWED))
      .flatMap(([, at]) => at);
    expect(stray, 'use errorMessage(err, "fallback")').toEqual([]);
  });

  it("every allowed file still reads rawMessage", () => {
    expect(Object.keys(ALLOWED).filter((f) => !reads.has(f))).toEqual([]);
  });
});
