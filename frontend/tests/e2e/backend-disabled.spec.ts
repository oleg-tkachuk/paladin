/**
 * Disabled storage backends in the UI. A disabled backend must be visibly badged, must NOT
 * be selectable as a working scope in the ScopePicker, and the operator
 * must be able to toggle its state from the /storage-backends admin page
 * with the change reflected promptly.
 *
 * Per-test fixture: one backend seeded then disabled via
 * BackendService.SetBackendEnabled (makeDisabledBackend(), which also deletes
 * the backend on teardown — 160 of them accumulated before it did).
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

test.describe("US3 — disabled backends in the UI", () => {
  test("admin page badges a disabled backend and can re-enable it", async ({
    page,
    makeDisabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeDisabledBackend();

    await gotoSettled(page, "/storage-backends");

    // The row for our seeded backend shows the "Disabled" badge.
    const row = page.getByRole("row", { name: new RegExp(be.backendId) });
    await expect(row).toBeVisible();
    await expect(row.getByText(/^Disabled$/)).toBeVisible();

    // Toggling flips it to Enabled within a couple of seconds (SC-007).
    await row.getByRole("button", { name: /^Enable$/ }).click();
    await expect(row.getByText(/^Enabled$/)).toBeVisible({ timeout: 2000 });
  });

  test("scope picker marks a disabled backend non-selectable", async ({
    page,
    makeDisabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeDisabledBackend();

    // The ScopePicker fetched backends on mount (at login), before the
    // seed ran — reload so its list includes the freshly-seeded backend.
    await page.reload();

    const trigger = page.getByRole("button", { name: /^Scope picker —/ });
    await trigger.click();

    // Open the Backend row's command palette.
    await page.getByRole("button", { name: /^Switch backend —/ }).click();

    // The disabled backend appears with the "Disabled" badge.
    const option = page
      .getByRole("option", { name: new RegExp(be.backendId) })
      .first();
    await expect(option).toBeVisible();
    await expect(option.getByText(/^Disabled$/)).toBeVisible();

    // Selecting it is a no-op: the topbar breadcrumb must NOT switch to
    // the disabled backend id.
    await option.click();
    await expect(trigger).not.toHaveAttribute(
      "aria-label",
      new RegExp(`backend ${be.backendId}`),
    );
  });
});
