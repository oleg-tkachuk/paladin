/**
 * M2M tokens — the page that mints service credentials.
 *
 * A token created here is a bearer credential for the planes it names, and the
 * secret is shown exactly once. That makes two properties worth holding still,
 * and neither is visible from the page alone:
 *
 *   - pressing Create actually minted something (the page only ever shows a
 *     prefix afterwards, so a create that silently failed and a create that
 *     succeeded look similar once the dialog closes);
 *   - Revoke is a state change on the server, not a row disappearing from a
 *     list.
 *
 * Both are checked by listing the tenant's tokens through the admin API.
 * Nothing had opened this page before.
 *
 * Everything happens in the CALLER's own tenant. That is now a choice about
 * what this spec is for, not a workaround: the handler's `scopeToTenant`
 * refuses a cross-tenant list with PermissionDenied unless the caller holds
 * platform.admin, and pins the acting tenant when they do — so a
 * platform.admin listing another tenant gets that tenant's tokens, verified
 * against the compose stack on 2026-08-30. The comment that used to sit here
 * said such a list "comes back empty rather than refused" and pointed at a
 * BACKLOG entry; the entry is gone because the gap was closed, and the
 * description outlived it. Cross-tenant behaviour is covered where it belongs,
 * in apitokenh/list_scoping_test.go, which drives both roles against List and
 * Revoke directly and asserts the acting tenant the handler hands to RLS.
 */
import type { Page } from "@playwright/test";
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  m2mTokensOf,
  seedAdminTenantID,
  revokeM2MToken,
} from "./fixtures/seed";
import { ConnectError, Code } from "@connectrpc/connect";

function tokensURL(tenantId: string): string {
  return `/tenants/${tenantId}/m2m-tokens`;
}

// The Name field, by its accessible name. The dialog moved onto the shared FormField,
// which generates the input's id, and these tests kept looking for the
// hard-coded #m2m-name it used to carry — so every one of them failed the day
// the page first ran against a deployment that serves it. A label is what an
// operator reads; it is also the association FormField exists to make.
function tokenName(page: Page) {
  // By accessible name, not getByLabel: the label's text is "Name*", the
  // asterisk aria-hidden, so only the role query sees what a reader hears.
  return page
    .getByRole("dialog")
    .getByRole("textbox", { name: "Name", exact: true });
}

test.describe("M2M tokens", () => {
  // api_token is an OPTIONAL subsystem, off in the shipped chart and in
  // backend/configs/compose.yaml. The e2e compose stack turns it on so this
  // page can be exercised at all; a cluster generally has not. Skip rather
  // than fail there — the page cannot misbehave in a deployment that does not
  // serve it, and a red suite would say something untrue about the build.
  test.beforeEach(async () => {
    try {
      await m2mTokensOf(await seedAdminTenantID());
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unimplemented) {
        test.skip(true, "api_token subsystem is disabled in this deployment");
      }
      throw err;
    }
  });

  test("creating a token mints it and reveals the secret once", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const before = await m2mTokensOf(tenantId);
    const beforeNames = new Set(before.map((t) => t.displayName));

    await gotoSettled(page, tokensURL(tenantId));
    await page.getByRole("button", { name: /New token/i }).click();

    const name = `ci-uploader-${Date.now().toString(36)}`;
    await tokenName(page).fill(name);
    await page.getByRole("button", { name: /Create token/i }).click();

    // The reveal panel is the only time the secret exists in the UI. Its
    // presence is the page's contract with the operator: copy it now or lose
    // it, which is why the dialog will not close until they say they have.
    const secret = page.locator("input[readonly]").first();
    await expect(secret).toBeVisible({ timeout: 15_000 });
    await expect(secret).toHaveValue(/^paladin_pat_/);
    await expect(page.getByRole("button", { name: /^Done$/ })).toBeDisabled();

    // The assertion the page cannot make: a credential now exists on the
    // server, under the name that was typed.
    // The tenant is shared with the rest of the suite, so the assertion is
    // "this token appeared", not "there is exactly one".
    await expect
      .poll(
        async () =>
          (await m2mTokensOf(tenantId)).some((t) => t.displayName === name),
        { timeout: 15_000 },
      )
      .toBe(true);
    const minted = (await m2mTokensOf(tenantId)).find(
      (t) => t.displayName === name,
    )!;
    expect(beforeNames.has(name)).toBe(false);
    expect(minted.revoked).toBe(false);
    // Audience defaults to the data plane; a token pinned to nothing would be
    // a token that works everywhere.
    expect(minted.audience.length).toBeGreaterThan(0);

    await revokeM2MToken(minted.name);
  });

  test("the reveal cannot be dismissed until the secret is acknowledged", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();

    await gotoSettled(page, tokensURL(tenantId));
    await page.getByRole("button", { name: /New token/i }).click();
    await tokenName(page).fill(`ack-${Date.now().toString(36)}`);
    await page.getByRole("button", { name: /Create token/i }).click();

    const done = page.getByRole("button", { name: /^Done$/ });
    await expect(done).toBeDisabled({ timeout: 15_000 });

    // Ticking the box is the operator saying the secret is safe. Until then
    // the only exit would lose a credential that already exists server-side.
    await page.getByRole("checkbox").check();
    await expect(done).toBeEnabled();
    await done.click();
    await expect(secretPanel(page)).toHaveCount(0, { timeout: 15_000 });

    for (const t of await m2mTokensOf(tenantId)) {
      if (!t.revoked) await revokeM2MToken(t.name);
    }
  });

  test("revoking a token marks it revoked rather than deleting it", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();

    await gotoSettled(page, tokensURL(tenantId));
    await page.getByRole("button", { name: /New token/i }).click();
    const name = `to-revoke-${Date.now().toString(36)}`;
    await tokenName(page).fill(name);
    await page.getByRole("button", { name: /Create token/i }).click();
    await expect(page.getByRole("button", { name: /^Done$/ })).toBeVisible({
      timeout: 15_000,
    });
    await page.getByRole("checkbox").check();
    await page.getByRole("button", { name: /^Done$/ }).click();

    await page
      .getByRole("button", { name: new RegExp(`Revoke ${name}`) })
      .click();
    await page.getByRole("button", { name: /^Revoke$/ }).click();

    // Revocation keeps the row: an operator auditing "which credentials
    // existed" needs the revoked ones too, and a deleted row answers nothing.
    await expect
      .poll(
        async () => {
          const tokens = await m2mTokensOf(tenantId);
          return tokens.find((t) => t.displayName === name)?.revoked;
        },
        { timeout: 15_000 },
      )
      .toBe(true);
  });
});

function secretPanel(page: import("@playwright/test").Page) {
  return page.locator("input[readonly]");
}
