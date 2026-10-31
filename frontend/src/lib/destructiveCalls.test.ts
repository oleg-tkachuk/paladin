import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

// Every call that destroys, revokes, cancels or resets something on the
// server, and how the console asks before making it.
//
// When this was measured, 4 of 19 asked nothing: Reset password (the user's
// current password stopped working at once), Cancel on a running operation,
// Clear on a tenant's default bucket (every bare-name create then fails), and
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
  "components/features/buckets/BucketDeleteDialog.tsx::deleteBucket":
    "it is the confirmation",
  "components/features/buckets/BucketGrantsCard.tsx::revokeBucketGrant":
    "ConfirmModal",
  "components/features/oauth/ConnectedApplications.tsx::revokeOAuthGrant":
    "ConfirmModal",
  "components/features/mcp/MCPAuthorization.tsx::deleteOAuthClient":
    "ConfirmModal",
  "app/collections/page.tsx::deleteCollection": "AlertDialog on the page",
  "app/storage-backends/[backendId]/BackendActions.tsx::deleteBackend":
    "ConfirmModal, the backend's id typed",
  "app/storage-backends/[backendId]/BackendActions.tsx::rotateCredentials":
    "RotateCredentialsDialog",
  "app/tenants/TenantDeleteDialog.tsx::deleteTenant":
    "the call is made from the dialog itself",
  "app/tenants/[id]/capabilities/RevokeCapabilityDialog.tsx::revoke":
    "the call is made from the dialog itself",
  "app/tenants/[id]/capabilities/RevokeBiscuitCopyDialog.tsx::revokeBiscuit":
    "the call is made from the dialog itself",
  "app/tenants/[id]/capabilities/RevokeRunDialog.tsx::revokeRun":
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
    "CancelOperationButton's ConfirmModal on each row",
  "app/operations/page.tsx::cancelOperation":
    "CancelOperationButton's ConfirmModal on each row",
  "components/features/ObjectDetailView.tsx::purgeObject": "ConfirmModal",
  "components/features/users/UserRowActions.tsx::deleteUser":
    "ConfirmModal, the user's login typed",
  "components/features/users/UserScopesCard.tsx::revokeScopes":
    "ConfirmModal, which says when the last scope leaves the user unscoped",
  "components/features/users/UserScopesCard.tsx::revoke": "ConfirmModal",
  "components/features/users/UserRowActions.tsx::resetPassword": "ConfirmModal",
};

// Of those, the calls nothing in the console undoes: a purge, a delete, a
// revoke. Each one asks for something typed before it goes (ConfirmModal's
// typeToConfirm, as Argo CD asks for an application's name), or says here
// why it need not. A new one fails until it is in TYPED or UNTYPED.
const IRREVERSIBLE =
  /^(delete|purge|revoke|permanentlyDelete|hardDelete|emptyTrash)([A-Z][A-Za-z]*)?$/;

/** "file::callee" → the file whose ConfirmModal passes typeToConfirm. */
const TYPED: Record<string, string> = {
  "app/trash/page.tsx::purgeTenant": "app/trash/page.tsx",
  "components/features/buckets/BucketDeleteDialog.tsx::deleteBucket":
    "components/features/buckets/BucketDeleteDialog.tsx",
  "app/collections/page.tsx::deleteCollection":
    "components/features/collections/CollectionDeleteDialog.tsx",
  "app/tenants/[id]/collections/page.tsx::deleteCollection":
    "components/features/collections/CollectionDeleteDialog.tsx",
  "app/storage-backends/[backendId]/BackendActions.tsx::deleteBackend":
    "app/storage-backends/[backendId]/BackendActions.tsx",
  "app/tenants/[id]/collections/[name]/objects/page.tsx::purgeObject":
    "app/tenants/[id]/collections/[name]/objects/page.tsx",
  "app/tenants/[id]/collections/[name]/trash/page.tsx::purgeObject":
    "app/tenants/[id]/collections/[name]/trash/page.tsx",
  "components/features/ObjectDetailView.tsx::purgeObject":
    "components/features/ObjectDetailView.tsx",
  "components/features/users/UserRowActions.tsx::deleteUser":
    "components/features/users/UserRowActions.tsx",
  "components/features/mcp/MCPAuthorization.tsx::deleteOAuthClient":
    "components/features/mcp/MCPAuthorization.tsx",
  "app/tenants/[id]/event-subscriptions/page.tsx::deleteSubscription":
    "app/tenants/[id]/event-subscriptions/page.tsx",
  "app/tenants/[id]/capabilities/RevokeCapabilityDialog.tsx::revoke":
    "app/tenants/[id]/capabilities/RevokeCapabilityDialog.tsx",
  "app/tenants/[id]/capabilities/RevokeRunDialog.tsx::revokeRun":
    "app/tenants/[id]/capabilities/RevokeRunDialog.tsx",
  "app/tenants/[id]/m2m-tokens/RevokeTokenDialog.tsx::revoke":
    "app/tenants/[id]/m2m-tokens/RevokeTokenDialog.tsx",
};

/** "file::callee" → why nothing is typed before it. */
const UNTYPED: Record<string, string> = {
  "app/tenants/TenantDeleteDialog.tsx::deleteTenant":
    "it moves the tenant to the trash, and a restore undoes it",
  "app/tenants/[id]/capabilities/RevokeBiscuitCopyDialog.tsx::revokeBiscuit":
    "the copy itself is pasted, which is more than a typed name asks",
  "components/features/buckets/BucketGrantsCard.tsx::revokeBucketGrant":
    "a grant can be given again, the same as before",
  "components/features/oauth/ConnectedApplications.tsx::revokeOAuthGrant":
    "the user's own consent; the application asks for it again",
  "components/features/users/UserScopesCard.tsx::revokeScopes":
    "scopes can be granted again, the same as before",
  "components/features/users/UserScopesCard.tsx::revoke":
    "scopes can be granted again, the same as before",
};

const SRC = join(__dirname, "..");

/** Whether a file gives some ConfirmModal a typeToConfirm. */
function passesTypeToConfirm(rel: string): boolean {
  const file = join(SRC, rel);
  const sf = ts.createSourceFile(
    file,
    readFileSync(file, "utf8"),
    ts.ScriptTarget.Latest,
    true,
  );
  const visit = (node: ts.Node): boolean => {
    if (
      (ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) &&
      node.tagName.getText(sf) === "ConfirmModal" &&
      node.attributes.properties.some(
        (a) => ts.isJsxAttribute(a) && a.name.getText(sf) === "typeToConfirm",
      )
    ) {
      return true;
    }
    return ts.forEachChild(node, visit) ?? false;
  };
  return visit(sf);
}

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

  it("every irreversible call asks for something typed, or says why not", () => {
    const callee = (c: string) => c.slice(c.lastIndexOf("::") + 2);
    const undecided = [...calls].filter(
      (c) => IRREVERSIBLE.test(callee(c)) && !(c in TYPED) && !(c in UNTYPED),
    );
    expect(
      undecided,
      "pass typeToConfirm to its ConfirmModal and add it to TYPED, or say in UNTYPED why it need not",
    ).toEqual([]);
  });

  it("every TYPED call's confirmation passes typeToConfirm", () => {
    const untyped = Object.entries(TYPED)
      .filter(([, file]) => !passesTypeToConfirm(file))
      .map(([call]) => call);
    expect(untyped).toEqual([]);
  });

  it("every TYPED and UNTYPED entry still names a call", () => {
    const stale = [...Object.keys(TYPED), ...Object.keys(UNTYPED)].filter(
      (k) => !calls.has(k),
    );
    expect(stale, "remove entries whose call is gone").toEqual([]);
  });
});
