/**
 * Deleting an OAuth client from the console (/mcp/access): a client that
 * registered itself is removed with the grants issued to it, so its refresh
 * token is refused afterwards, as the token of an unknown client; a client the configuration seeds offers no
 * delete, because the next boot would register it again.
 */
import { test, expect } from "@playwright/test";

import { loginAsAdmin } from "./fixtures/auth";
import {
  AUDIENCE_ADMIN,
  CLIENT_ID,
  HTTP_UNAUTHORIZED,
  SCOPE_READ,
  grant,
  registerClient,
  revokeIssuedGrants,
} from "./fixtures/oauth";
import { IAM_URL } from "./fixtures/seed";

const ACCESS_PAGE = "/mcp/access";
const DELETE = "Delete client";

test.describe("OAuth client deletion", () => {
  // Give up every grant the test was issued, so none outlives it.
  test.afterEach(async ({ request }) => {
    await revokeIssuedGrants(request);
  });

  test("a self-registered client goes with its grant", async ({
    page,
    request,
  }) => {
    const clientId = await registerClient(request, "e2e delete", [
      AUDIENCE_ADMIN,
    ]);
    const tok = await grant(request, [SCOPE_READ], AUDIENCE_ADMIN, clientId);

    await loginAsAdmin(page);
    await page.goto(ACCESS_PAGE);
    const row = page.getByRole("row", { name: new RegExp(clientId) });
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.click();
    const record = page.getByRole("dialog");
    await expect(record).toContainText("by the client itself");
    await record.getByRole("button", { name: DELETE }).click();
    const confirm = page.getByRole("alertdialog");
    await expect(confirm).toContainText("every tenant");
    await confirm
      .getByRole("textbox", { name: "Type delete to confirm" })
      .fill("delete");
    await confirm.getByRole("button", { name: DELETE }).click();
    // The open confirmation hides the page from the accessibility tree, so
    // the row reads as gone before the delete has answered: wait it out.
    await expect(confirm).toBeHidden();
    await expect(row).toHaveCount(0);

    const res = await request.post(`${IAM_URL}/oauth/token`, {
      form: {
        grant_type: "refresh_token",
        client_id: clientId,
        refresh_token: tok.refresh_token,
      },
    });
    // The client is unknown now, which the token endpoint answers before it
    // reads the refresh token.
    expect(res.status(), await res.text()).toBe(HTTP_UNAUTHORIZED);
  });

  test("a seeded client offers no delete", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(ACCESS_PAGE);
    const row = page.getByRole("row", { name: new RegExp(CLIENT_ID) });
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.click();
    const record = page.getByRole("dialog");
    await expect(record).toContainText("by the configuration");
    await expect(record.getByRole("button", { name: DELETE })).toHaveCount(0);
  });
});
