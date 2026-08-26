/* eslint-disable react-hooks/rules-of-hooks --
 * Playwright fixtures take a callback named `use`; the React lint rule sees
 * the name and assumes a hook. There is no React here. */
import { test as base } from "@playwright/test";
import {
  seedTenant,
  seedTenantMembership,
  deleteTenants,
  type SeededMembership,
  type SeededTenant,
} from "./seed";

/**
 * A `test` that cleans up the tenants it created.
 *
 * Use `makeTenant()` instead of calling seedTenant() directly and the tenant is
 * deleted when the test ends, pass or fail.
 *
 * This exists because the alternative was measured: after a day of runs the
 * cluster held 500 tenants, every list page rendered hundreds of rows, and
 * clicks began to be dropped while React worked through them. Tests were
 * making the app they test slower, and the resulting flake was diagnosed three
 * times as something else before the row count was noticed.
 */
export const test = base.extend<{
  makeTenant: (opts?: { slugPrefix?: string }) => Promise<SeededTenant>;
  makeMembership: () => Promise<SeededMembership>;
}>({
  makeTenant: async ({}, use) => {
    const created: string[] = [];
    await use(async (opts) => {
      const t = await seedTenant(opts);
      created.push(t.tenantId);
      return t;
    });
    // Best-effort: a teardown failure must not fail a test that passed.
    if (created.length) await deleteTenants(created).catch(() => {});
  },

  // seedTenantMembership creates a whole second tenant (slug switch-*) plus a
  // users row inside it, and had no teardown at all — 34 of them had piled up.
  makeMembership: async ({}, use) => {
    const created: string[] = [];
    await use(async () => {
      const m = await seedTenantMembership();
      created.push(m.tenantId);
      return m;
    });
    if (created.length) await deleteTenants(created).catch(() => {});
  },
});

export { expect } from "@playwright/test";
