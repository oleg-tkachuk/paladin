/**
 * Object detail — the page an operator lands on to answer "what is this
 * object, and is it safe to act on it".
 *
 * The object list has covered the row-menu actions since US6; the detail page
 * itself had never been opened. It is the only surface that shows an object's
 * identity and state together, and its actions are gated on that state — a
 * page that renders a DELETED object as if it were live invites exactly the
 * action the trash exists to prevent.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  seedAdminTenantID,
  seedObject,
  seedPhysicalBucket,
} from "./fixtures/seed";

function detailURL(tenantId: string, collection: string, key: string): string {
  return `/tenants/${tenantId}/collections/${encodeURIComponent(collection)}/objects/${encodeURIComponent(key)}`;
}

test.describe("Object detail", () => {
  test("renders the object's identity, not just its file name", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    // A physical bucket: the detail page is about an object that exists, and
    // seeding one puts real bytes on the backend.
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });
    const obj = await seedObject({
      tenantId,
      collection: collection.collection,
      key: `e2e/nested/path/report.txt`,
    });

    await gotoSettled(
      page,
      detailURL(tenantId, collection.collection, obj.key),
    );

    // The heading shows the file name — the last segment — while the full key
    // is the description. Both matter: two objects in different folders share
    // a file name, and the key is what every API call takes.
    await expect(
      page.getByRole("heading", { name: /report\.txt/ }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByText(obj.key, { exact: true }).first(),
    ).toBeVisible();
  });

  test("offers the way back to the object list", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    // A physical bucket: the detail page is about an object that exists, and
    // seeding one puts real bytes on the backend.
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });
    const obj = await seedObject({
      tenantId,
      collection: collection.collection,
    });

    await gotoSettled(
      page,
      detailURL(tenantId, collection.collection, obj.key),
    );
    const back = page.getByRole("button", { name: "Back to Objects" });
    await expect(back).toBeVisible({ timeout: 15_000 });
    await back.click();

    // A detail page reached by URL rather than by clicking still has to know
    // where "back" is — the parent collection, not the browser's history.
    await expect(page).toHaveURL(
      new RegExp(
        `/collections/${encodeURIComponent(collection.collection).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}/objects$`,
      ),
      { timeout: 15_000 },
    );
  });
});
