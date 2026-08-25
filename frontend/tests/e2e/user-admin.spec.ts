/**
 * US7 — User administration and password change.
 *
 * Both flows existed only in the API. /users could list principals and
 * nothing else — seven of UserService's eight RPCs had no caller — so adding
 * a colleague meant an operator with a database connection. AuthService
 * exposed ChangePassword and the console never called it, leaving a user no
 * way to rotate their own credential.
 *
 * These tests drive the console, not the API: the point is that an operator
 * can do the thing, which is exactly what was missing.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { uniqueSlug } from "./fixtures/unique";

test.describe("US7 — User administration", () => {
  test("an operator can create a user and see it in the list", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/users");

    const subject = `${uniqueSlug("e2e-user")}@example.test`;
    await page.getByRole("button", { name: "New user" }).click();

    await page.getByLabel("Subject").fill(subject);
    await page.getByLabel("Display name").fill("E2E Created User");
    // The server's floor is 12 characters; anything shorter is refused
    // before it reaches the handler.
    await page.getByLabel("Initial password").fill("e2e-not-a-secret-2026");
    await page.getByRole("button", { name: "Create user" }).click();

    await expect(page.getByText(subject).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test("the create dialog refuses a password the server would reject", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/users");
    await page.getByRole("button", { name: "New user" }).click();

    await page.getByLabel("Subject").fill("too-short@example.test");
    await page.getByLabel("Initial password").fill("short");

    // Refused client-side so the operator sees why inline rather than
    // through a round trip; the server still enforces the same floor.
    await expect(
      page.getByRole("button", { name: "Create user" }),
    ).toBeDisabled();
    await expect(
      page.getByText(/at least 12 characters/i).first(),
    ).toBeVisible();
  });

  test("a created user can be disabled from its row menu", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/users");

    const subject = `${uniqueSlug("e2e-disable")}@example.test`;
    await page.getByRole("button", { name: "New user" }).click();
    await page.getByLabel("Subject").fill(subject);
    await page.getByLabel("Initial password").fill("e2e-not-a-secret-2026");
    await page.getByRole("button", { name: "Create user" }).click();

    const row = page.getByRole("row", { name: new RegExp(subject) });
    await expect(row).toBeVisible({ timeout: 10_000 });
    await expect(row.getByText("active")).toBeVisible();

    await row.getByRole("button", { name: `Actions for ${subject}` }).click();
    await page.getByText("Disable", { exact: true }).click();

    await expect(
      page
        .getByRole("row", { name: new RegExp(subject) })
        .getByText("disabled"),
    ).toBeVisible({ timeout: 10_000 });
  });
});

test.describe("US7 — Password change", () => {
  test("the profile page offers a password change with a confirm field", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/profile");

    // getByText, not getByRole("heading"): CardTitle renders a <div>, so
    // section titles are not headings and cannot be navigated to as such.
    // BACKLOG carries that; asserting on the role here would pin the bug
    // rather than the feature.
    await expect(page.getByText("Password", { exact: true })).toBeVisible();
    await page.getByLabel("Current password").fill("e2e-not-a-secret-2026");
    await page
      .getByLabel("New password", { exact: true })
      .fill("a-longer-replacement-2026");
    await page.getByLabel("Confirm new password").fill("mismatched-value-2026");

    // A mismatch is caught before the request: sending it would burn the
    // rate limiter on an error the form already knows about.
    await expect(page.getByText(/does not match/i)).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Change password" }),
    ).toBeDisabled();
  });
});
