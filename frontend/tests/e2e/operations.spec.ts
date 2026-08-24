/**
 * Background operations — the Any-carrying surface.
 *
 * ListOperations returns operations.metadata as a google.protobuf.Any, packed
 * as a Struct by the server. Any carries its payload's type as a URL, so every
 * JSON hop needs a type registry to resolve it — and the console has two: the
 * BFF bridge re-encodes each plane response for the browser, and the browser
 * decodes it. Neither had one.
 *
 * The result was a 500 on every ListOperations that carried an operation, with
 * the drawer and the dashboard widget dead. It stayed hidden while the listing
 * was empty, then broke the moment any batch job ran. Calling the plane
 * directly returned 200 throughout, which is what made it read as a server
 * bug for so long.
 *
 * These drive the console, not the plane, because the plane was never wrong.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedBucket,
  seedCollection,
  seedObject,
  seedBatchTagOperation,
} from "./fixtures/seed";

test.describe("Background operations", () => {
  test("the operations listing decodes metadata rather than failing", async ({
    page,
  }) => {
    await loginAsAdmin(page);

    // An operation with a metadata payload is the precondition — the bug is
    // invisible against an empty listing, which is exactly how it survived.
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket({ provision: true });
    const collection = await seedCollection({ tenantId, bucket });
    const obj = await seedObject({
      tenantId,
      collection: collection.collection,
    });
    await seedBatchTagOperation({
      tenantId,
      collection: collection.collection,
      objectName: obj.name,
    });

    const failures: string[] = [];
    page.on("response", (r) => {
      if (r.url().includes("ListOperations") && r.status() >= 500) {
        failures.push(`${r.status()} ${r.url()}`);
      }
    });

    await page.goto("/tenants");

    // The drawer is where an operator actually reads this.
    await page.getByRole("button", { name: /Background operations/i }).click();

    // Any 5xx here is the defect: the bridge could not re-encode the Any and
    // answered 500 while the plane had returned a perfectly good response.
    await expect
      .poll(() => failures, { timeout: 15_000, message: "ListOperations 5xx" })
      .toEqual([]);
  });

  test("the failed-operations widget filter is accepted by the server", async ({
    page,
  }) => {
    await loginAsAdmin(page);

    const rejected: string[] = [];
    page.on("response", async (r) => {
      if (r.url().includes("ListOperations") && r.status() === 400) {
        let b = "";
        try {
          b = (await r.text()).slice(0, 200);
        } catch {
          /* streamed */
        }
        rejected.push(b);
      }
    });

    await page.goto("/");
    await page.waitForTimeout(5_000);

    // The dashboard's failed-ops widget filters on error_message. A field the
    // CEL schema does not declare is rejected outright — the widget then shows
    // nothing, with the reason only visible in the console.
    expect(rejected, `filter rejected: ${rejected.join(" | ")}`).toEqual([]);
  });
});
