/**
 * Upload — the only object-ingest path a human uses.
 *
 * Every other test in this suite puts bytes in through presigned PUTs in the
 * fixtures, which is the machine path. This page is the one an operator
 * touches, and it had never been opened: 474 lines of destination picking,
 * tag entry and drop-zone state that nothing exercised.
 *
 * The lock — "Select a destination first", file input disabled — is real but
 * NOT asserted here: page.tsx auto-selects the tenant's first Collection on
 * mount, so the locked state only appears for a tenant that has none. On the
 * shared platform tenant that depends on what the rest of the suite has
 * created, and a test whose premise is the environment passes for the wrong
 * reason. (It did: this file's first draft asserted the lock, passed alone,
 * and failed in the full run.)
 *
 * What is asserted instead is the guarantee an operator actually relies on:
 * the page names the destination before it accepts anything, and the file
 * ends up in that Collection and not another.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  seedAdminTenantID,
  seedPhysicalBucket,
  objectsOf,
} from "./fixtures/seed";

test.describe("Upload", () => {
  test("the drop zone names the destination it will upload to", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, "/upload");
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: collection.displayName }).click();

    // Dropping a file is irreversible from the operator's side — the bytes are
    // on the backend before anything else happens. The picker therefore keeps
    // showing the chosen destination while the zone is armed, rather than
    // collapsing back to a placeholder once the menu closes.
    await expect(page.getByRole("combobox").first()).toContainText(
      collection.displayName,
      { timeout: 15_000 },
    );
    await expect(page.locator('input[type="file"]')).toBeEnabled();
  });

  test("choosing a Collection unlocks it and names the destination", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, "/upload");
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: collection.displayName }).click();

    await expect(page.locator('input[type="file"]')).toBeEnabled({
      timeout: 15_000,
    });
    await expect(
      page.getByRole("heading", { name: /Select a destination first/i }),
    ).toHaveCount(0);
  });

  test("an uploaded file lands in the Collection it was aimed at", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, "/upload");
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: collection.displayName }).click();

    const fileName = `e2e-upload-${Date.now().toString(36)}.txt`;
    await page.locator('input[type="file"]').setInputFiles({
      name: fileName,
      mimeType: "text/plain",
      buffer: Buffer.from("uploaded through the console\n"),
    });

    // The page's own progress is not the assertion — the object is. A queue
    // entry that says "done" while nothing reached the collection is the
    // failure this test exists to catch.
    await expect
      .poll(
        async () =>
          (await objectsOf(tenantId, collection.collection)).some((k) =>
            k.endsWith(fileName),
          ),
        { timeout: 30_000 },
      )
      .toBe(true);
  });
});
