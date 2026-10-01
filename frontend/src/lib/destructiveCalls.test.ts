import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Every call that destroys, revokes, cancels or resets something on the
// server, and how the console asks before making it.
//
// When this was measured, 4 of 19 asked nothing: Reset password (the user's
// current password stopped working at once), Cancel on a running operation,
// Clear on a tenant's default route (every bare-name create then fails), and
// Reset daily counters. Delete sat beside Reset password and had always asked.
//
// A text rule cannot tell whether a confirmation guards a call: it is often a
// dialog in another component. So this does not decide that. It makes adding a
// destructive call a decision someone writes down — a new call site fails here
// until GUARDED says how it is confirmed — and each surface's own test pins
// that the confirmation is really there.

/** Calls whose name says they destroy, revoke, cancel or reset. */
const DESTRUCTIVE =
  /^(delete|purge|revoke|clear|cancel|reset|rotate|drain|undrain|permanentlyDelete|hardDelete|emptyTrash)([A-Z][A-Za-z]*)?$/;
/** Local helpers with those names that touch nothing on the server. */
const LOCAL =
  /^(clearTimeout|clearInterval|clearSessionCookie|clearSelection|resetIssueForm|resetForm|clearAllTokens|reset|clear|delete)$/;

/** "file::callee" → how the console asks before the call. */
const GUARDED: Record<string, string> = {
  "app/buckets/page.tsx::deleteBucket": "AlertDialog on the page",
  "app/collections/page.tsx::deleteCollection": "AlertDialog on the page",
  "app/storage-backends/[backendId]/BackendActions.tsx::deleteBackend":
    "DeleteBackendDialog",
  "app/storage-backends/[backendId]/BackendActions.tsx::rotateCredentials":
    "RotateCredentialsDialog",
  "app/tenants/TenantDeleteDialog.tsx::deleteTenant":
    "the call is made from the dialog itself",
  "app/tenants/[id]/buckets/page.tsx::deleteBucket": "AlertDialog on the page",
  "app/tenants/[id]/capabilities/RevokeCapabilityDialog.tsx::revoke":
    "the call is made from the dialog itself",
  "app/tenants/[id]/collections/[name]/objects/page.tsx::purgeObject":
    "confirm state on the page",
  "app/tenants/[id]/collections/[name]/trash/page.tsx::purgeObject":
    "AlertDialog, single and bulk",
  "app/tenants/[id]/collections/page.tsx::deleteCollection":
    "AlertDialog on the page",
  "app/tenants/[id]/default-binding/page.tsx::clearTenantDefaultBinding":
    "ConfirmModal",
  "app/tenants/[id]/event-subscriptions/page.tsx::deleteSubscription":
    "ConfirmModal",
  "app/tenants/[id]/m2m-tokens/RevokeTokenDialog.tsx::revoke":
    "the call is made from the dialog itself",
  "app/tenants/[id]/quotas/page.tsx::resetUsage": "ConfirmModal",
  "app/trash/page.tsx::purgeTenant": "AlertDialog on the page",
  "components/BackgroundOpsDrawer.tsx::cancelOperation":
    "ConfirmModal on each row",
  "components/features/ObjectDetailView.tsx::purgeObject": "ConfirmModal",
  "components/features/users/UserRowActions.tsx::deleteUser": "Modal",
  "components/features/users/UserRowActions.tsx::resetPassword": "ConfirmModal",
};

const SRC = join(__dirname, "..");

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

/** Destructive call sites in UI code — hooks and lib define them, not call. */
function destructiveCalls(): Set<string> {
  const found = new Set<string>();
  for (const file of sourceFiles(SRC)) {
    const rel = relative(SRC, file);
    if (rel.startsWith("hooks/") || rel.startsWith("lib/")) continue;
    const sf = ts.createSourceFile(
      file,
      readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    const visit = (node: ts.Node) => {
      if (ts.isCallExpression(node)) {
        const callee = node.expression;
        if (
          ts.isPropertyAccessExpression(callee) &&
          /Client$/.test(callee.expression.getText(sf)) &&
          DESTRUCTIVE.test(callee.name.text)
        ) {
          found.add(`${rel}::${callee.name.text}`);
        } else if (
          ts.isIdentifier(callee) &&
          DESTRUCTIVE.test(callee.text) &&
          !LOCAL.test(callee.text)
        ) {
          found.add(`${rel}::${callee.text}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return found;
}

describe("destructive calls", () => {
  const calls = destructiveCalls();

  it("finds the calls it is meant to check", () => {
    expect(calls.size).toBeGreaterThan(15);
  });

  it("every destructive call says how it is confirmed", () => {
    const unguarded = [...calls].filter((c) => !(c in GUARDED));
    expect(
      unguarded,
      "ask before the call (ConfirmModal), then add it to GUARDED with how",
    ).toEqual([]);
  });

  it("every GUARDED entry still names a call", () => {
    const stale = Object.keys(GUARDED).filter((k) => !calls.has(k));
    expect(stale, "remove entries whose call is gone").toEqual([]);
  });
});
