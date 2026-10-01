import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// The query hooks below are state-only (errorContract.ts): a failed fetch sets
// `error` and leaves the list empty. A caller that takes the list and not the
// error renders a failed read exactly like "there is nothing" — a select with
// no options, "no buckets", a Set button disabled for want of a choice. When
// this was first measured, 24 of 33 call sites took a list without its error.
//
// So every call site that takes one of these lists must take `error` too, or
// be listed in ALLOWED with the reason its emptiness claims nothing.
//
// Matched on the AST, not on text: a destructuring spans lines, and a text
// rule would count comments and strings as callers.

const HOOK_LISTS: Record<string, string[]> = {
  useBackends: ["backends"],
  useBuckets: ["buckets"],
  useCollections: ["collections"],
  useMemberships: ["memberships"],
  useTenants: ["tenants"],
};

/**
 * Call sites whose list may stay error-blind, keyed "file::hook". Each reason
 * says what an empty list falls back to; an entry that no longer matches a
 * call site fails the second test, so this list cannot outlive its callers.
 */
const ALLOWED: Record<string, string> = {
  "app/storage-backends/[backendId]/buckets/[bucketId]/page.tsx::useBackends":
    "label only: a miss drops the backend name from the header; the bucket itself comes from its own query, which surfaces errors",
  "app/storage-backends/[backendId]/buckets/[bucketId]/page.tsx::useTenants":
    "label only: a miss shows the tenant id instead of its slug; the collections come from their own query, which surfaces errors",
  "app/policies/page.tsx::useTenants":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/policies/page.tsx::useBuckets":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/policies/page.tsx::useCollections":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/tenants/[id]/collections/[name]/page.tsx::useBuckets":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/tenants/[id]/default-binding/page.tsx::useBuckets":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/upload/page.tsx::useCollections":
    "waiting for the pages pass: a failed read still renders as empty here",
  "app/users/page.tsx::useTenants":
    "waiting for the pages pass: a failed read still renders as empty here",
  "components/layout/ScopePicker.tsx::useMemberships":
    "waiting for the ScopePicker pass: a failed read reads as 'no backends configured', 'no buckets' or a single tenant",
  "components/layout/ScopePicker.tsx::useBuckets":
    "waiting for the ScopePicker pass: a failed read reads as 'no backends configured', 'no buckets' or a single tenant",
  "components/layout/ScopePicker.tsx::useBackends":
    "waiting for the ScopePicker pass: a failed read reads as 'no backends configured', 'no buckets' or a single tenant",
};

const SRC = join(__dirname, "..");

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

type CallSite = { key: string; takesList: boolean; takesError: boolean };

function callSites(): CallSite[] {
  const sites: CallSite[] = [];
  for (const file of sourceFiles(SRC)) {
    const text = readFileSync(file, "utf8");
    if (!Object.keys(HOOK_LISTS).some((h) => text.includes(h))) continue;
    const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
    const visit = (node: ts.Node) => {
      if (
        ts.isVariableDeclaration(node) &&
        node.initializer &&
        ts.isCallExpression(node.initializer) &&
        ts.isIdentifier(node.initializer.expression) &&
        node.initializer.expression.text in HOOK_LISTS &&
        ts.isObjectBindingPattern(node.name)
      ) {
        const hook = node.initializer.expression.text;
        const fields = node.name.elements.map((el) =>
          (el.propertyName ?? el.name).getText(sf),
        );
        sites.push({
          key: `${relative(SRC, file)}::${hook}`,
          takesList: fields.some((f) => HOOK_LISTS[hook].includes(f)),
          takesError: fields.includes("error"),
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return sites;
}

describe("query hook error contract", () => {
  const sites = callSites();

  it("finds the call sites it is meant to check", () => {
    // A rename that hides every caller would otherwise pass vacuously.
    expect(sites.length).toBeGreaterThan(20);
  });

  it("every caller that takes a list takes its error, or says why not", () => {
    const blind = sites
      .filter((s) => s.takesList && !s.takesError && !(s.key in ALLOWED))
      .map((s) => s.key);
    expect(
      blind,
      "render `error` (ListLoadError) or add to ALLOWED with the reason",
    ).toEqual([]);
  });

  it("every allowed entry still names an error-blind call site", () => {
    const stale = Object.keys(ALLOWED).filter(
      (key) =>
        !sites.some((s) => s.key === key && s.takesList && !s.takesError),
    );
    expect(
      stale,
      "remove entries whose call site is gone or now takes error",
    ).toEqual([]);
  });
});
