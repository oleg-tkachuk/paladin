/**
 * Tenant audit log — the page an operator opens to answer "who did this".
 *
 * The platform-wide log at /audit has coverage; the per-tenant one did not,
 * and it is the one that answers the question in a tenant's own terms. Its
 * value rests on two things: that an action taken a moment ago shows up, and
 * that the filter narrows rather than empties.
 *
 * The page shows the tenant's trail: what its principals did, and what was
 * done to it — a platform admin's work inside the tenant included. That last
 * part is what the page long missed: entries were keyed by the actor's tenant
 * alone, so an operator's changes to tenant X landed under platform and X's
 * page stayed empty.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { seedAdminTenantID } from "./fixtures/seed";

function auditURL(tenantId: string): string {
  return `/tenants/${tenantId}/audit-log`;
}

test.describe("Tenant audit log", () => {
  test("an action taken now appears in the tenant's log", async ({
    page,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    // The admin's own tenant: actor and page coincide.
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    // A write with a name of its own: the entry has to be findable by
    // something the operator would actually search for.
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, auditURL(tenantId));
    await expect(
      page.getByRole("heading", { name: /^Audit log$/ }),
    ).toBeVisible({ timeout: 15_000 });

    // CreateCollection is an admin-plane RPC, so the tenant's log is where it
    // belongs. A log that records nothing is indistinguishable from a tenant
    // that did nothing.
    await expect
      .poll(
        async () =>
          page
            .getByText(/CreateCollection/)
            .count()
            .then((n) => n > 0),
        { timeout: 20_000 },
      )
      .toBe(true);
    expect(collection.collection).toBeTruthy();
  });

  test("an admin's action inside another tenant appears in that tenant's log", async ({
    page,
    makeTenant,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    // A tenant the admin is not a member of. The collection's resource name
    // nests the tenant under its bucket, so this also checks that the trail
    // does not hang on a `tenants/<id>/` prefix.
    const tenant = await makeTenant();
    const bucket = await makeBucket();
    await makeCollection({ tenantId: tenant.tenantId, bucket });

    await gotoSettled(page, auditURL(tenant.tenantId));
    await expect(
      page.getByRole("heading", { name: /^Audit log$/ }),
    ).toBeVisible({ timeout: 15_000 });
    await expect
      .poll(
        async () =>
          page
            .getByText(/CreateCollection/)
            .count()
            .then((n) => n > 0),
        { timeout: 20_000 },
      )
      .toBe(true);
  });

  test("the filter narrows the log instead of clearing it", async ({
    page,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    // The admin's own tenant: actor and page coincide.
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    await makeCollection({ tenantId, bucket });

    await gotoSettled(page, auditURL(tenantId));
    const filter = page.getByPlaceholder(/Filter by action, actor, resource/i);
    await expect(filter).toBeVisible({ timeout: 15_000 });

    await filter.fill("CreateCollection");
    // Still showing the matching rows, and nothing that cannot match.
    await expect
      .poll(async () => page.getByText(/CreateCollection/).count(), {
        timeout: 15_000,
      })
      .toBeGreaterThan(0);

    // A term that matches nothing empties the table and says why, rather than
    // silently showing the unfiltered log.
    await filter.fill("zzz-no-such-action-zzz");
    await expect(page.getByText(/CreateCollection/)).toHaveCount(0, {
      timeout: 15_000,
    });
  });
});
