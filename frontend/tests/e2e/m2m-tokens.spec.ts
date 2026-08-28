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
 * Everything happens in the CALLER's own tenant, deliberately. api_tokens
 * carries FORCE row-level security (`tenant_id = paladin_session_tenant_id()`),
 * so a list for any other tenant comes back empty rather than refused — see
 * BACKLOG, "A cross-tenant token list is empty rather than refused". An
 * earlier draft used a fresh tenant and passed only on the compose stack,
 * where the session tenant is unset and a second policy lets everything
 * through. It was testing that configuration, not the product.
 */
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
    await page.locator("#m2m-name").fill(name);
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
    await page.locator("#m2m-name").fill(`ack-${Date.now().toString(36)}`);
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
    await page.locator("#m2m-name").fill(name);
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
