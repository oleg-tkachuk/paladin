/**
 * Storage backend operational state — drain and maintenance.
 *
 * SetBackendReadOnly, SetBackendMaintenance and SetBackendEnabled are the
 * three RPCs whose resource_version was already required in the schema
 * (min_len=1, "OCC, required"), long before the rest caught up. They gate
 * whether a backend accepts writes at all, so a lost update here silently
 * re-opens a backend an operator drained.
 *
 * backend-disabled.spec.ts covers what a DISABLED backend does to the rest of
 * the console. This covers the transitions themselves.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedEnabledBackend, backendState } from "./fixtures/seed";

test.describe("Storage backend — drain and maintenance", () => {
  test("draining a backend makes it read-only and advances the version", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const be = await seedEnabledBackend();
    const before = await backendState(be.backendId);
    expect(before.readOnly).toBe(false);

    await page.goto("/storage-backends");
    const row = page.getByRole("row", { name: new RegExp(be.backendId) });
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row.getByRole("button", { name: /^Drain$/ }).click();

    await expect
      .poll(async () => (await backendState(be.backendId)).readOnly, {
        timeout: 15_000,
      })
      .toBe(true);

    const after = await backendState(be.backendId);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
    // Draining must not disable: a drained backend still serves reads, which
    // is the whole difference between drain and disable.
    expect(after.enabled).toBe(true);
  });

  test("undrain restores writes", async ({ page }) => {
    await loginAsAdmin(page);
    const be = await seedEnabledBackend();

    await page.goto("/storage-backends");
    const row = page.getByRole("row", { name: new RegExp(be.backendId) });
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row.getByRole("button", { name: /^Drain$/ }).click();
    await expect
      .poll(async () => (await backendState(be.backendId)).readOnly, {
        timeout: 15_000,
      })
      .toBe(true);

    // The button is the same control; its label follows the state, which is
    // what makes this a round trip rather than two unrelated clicks.
    await row.getByRole("button", { name: /^Undrain$/ }).click();
    await expect
      .poll(async () => (await backendState(be.backendId)).readOnly, {
        timeout: 15_000,
      })
      .toBe(false);
  });

  test("maintenance is independent of drain", async ({ page }) => {
    await loginAsAdmin(page);
    const be = await seedEnabledBackend();

    await page.goto("/storage-backends");
    const row = page.getByRole("row", { name: new RegExp(be.backendId) });
    await expect(row).toBeVisible({ timeout: 15_000 });

    // Maintenance has no per-row button — it is a bulk action, so the row is
    // selected first. That is a UI shape, not a different RPC: the bulk path
    // fans out over the same OCC-guarded SetBackendMaintenance.
    await page
      .getByRole("checkbox", { name: `Select ${be.backendId}` })
      .click();
    await page.getByRole("button", { name: "Bulk maintenance" }).click();

    await expect
      .poll(async () => (await backendState(be.backendId)).maintenance, {
        timeout: 15_000,
      })
      .toBe(true);

    // Three separate flags, three separate RPCs. Maintenance must not imply
    // read-only or disabled — an operator flipping one and getting three has
    // no way to express the state they actually wanted.
    const after = await backendState(be.backendId);
    expect(after.readOnly).toBe(false);
    expect(after.enabled).toBe(true);
  });
});
