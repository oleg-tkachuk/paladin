/* eslint-disable react-hooks/rules-of-hooks --
 * Playwright fixtures take a callback named `use`; the React lint rule sees
 * the name and assumes a hook. There is no React here. */
import { test as base } from "@playwright/test";
import {
  deleteBackends,
  deleteBuckets,
  deleteCollections,
  deleteTenants,
  seedBucket,
  seedCollection,
  seedDisabledBackend,
  seedEnabledBackend,
  seedTenant,
  seedTenantMembership,
  type SeededBackend,
  type SeededBucket,
  type SeededCollection,
  type SeededMembership,
  type SeededTenant,
} from "./seed";

/**
 * The `test` that cleans up after itself.
 *
 * This replaces the per-resource fixture modules (tenants, backends). They
 * could not be composed — a spec imports one `test` — so a spec that needed
 * both got neither, and the resources with no fixture at all (buckets,
 * collections, objects) were never cleaned by anyone. A run against the
 * cluster left 19 buckets and 14 collections behind; at that rate the
 * environment reaches the size that has already been diagnosed three times as
 * something else in about 45 runs.
 *
 * Teardown order is the documented one (docs/deletion-semantics.md): objects,
 * then collections, then buckets, then backends, then tenants. It lives in a
 * single fixture rather than one per resource because Playwright tears
 * fixtures down in reverse setup order, which depends on which the test
 * happened to touch first — and the FKs here are RESTRICT, so a wrong order
 * does not corrupt anything, it just silently leaves rows behind.
 */
/** Maker signatures, exported so a spec-local helper can take one. */
export type MakeTenant = (opts?: {
  slugPrefix?: string;
}) => Promise<SeededTenant>;
export type MakeBackend = (opts?: {
  idPrefix?: string;
}) => Promise<SeededBackend>;
export type MakeBucket = (opts?: {
  backendId?: string;
  bucketIdPrefix?: string;
  displayNamePrefix?: string;
  provision?: boolean;
}) => Promise<SeededBucket>;
export type MakeCollection = (opts: {
  tenantId: string;
  bucket: SeededBucket;
  collectionPrefix?: string;
}) => Promise<SeededCollection>;

interface Registry {
  tenants: string[];
  backends: string[];
  buckets: SeededBucket[];
  collections: SeededCollection[];
}

export const test = base.extend<{
  cleanup: Registry;
  makeTenant: MakeTenant;
  makeMembership: () => Promise<SeededMembership>;
  makeDisabledBackend: MakeBackend;
  makeEnabledBackend: () => Promise<SeededBackend>;
  makeBucket: MakeBucket;
  makeCollection: MakeCollection;
}>({
  cleanup: async ({}, use) => {
    const reg: Registry = {
      tenants: [],
      backends: [],
      buckets: [],
      collections: [],
    };
    await use(reg);
    // Best-effort throughout: a teardown failure must not fail a test that
    // passed. Each step reports what it could not remove.
    if (reg.collections.length) {
      await deleteCollections(reg.collections).catch(() => {});
    }
    if (reg.buckets.length) await deleteBuckets(reg.buckets).catch(() => {});
    if (reg.backends.length) await deleteBackends(reg.backends).catch(() => {});
    if (reg.tenants.length) await deleteTenants(reg.tenants).catch(() => {});
  },

  makeTenant: async ({ cleanup }, use) => {
    await use(async (opts) => {
      const t = await seedTenant(opts);
      cleanup.tenants.push(t.tenantId);
      return t;
    });
  },

  // seedTenantMembership creates a whole second tenant (slug switch-*) plus a
  // users row inside it, and had no teardown at all — 34 of them had piled up.
  makeMembership: async ({ cleanup }, use) => {
    await use(async () => {
      const m = await seedTenantMembership();
      cleanup.tenants.push(m.tenantId);
      return m;
    });
  },

  makeDisabledBackend: async ({ cleanup }, use) => {
    await use(async (opts) => {
      const be = await seedDisabledBackend(opts);
      cleanup.backends.push(be.backendId);
      return be;
    });
  },

  makeEnabledBackend: async ({ cleanup }, use) => {
    await use(async () => {
      const be = await seedEnabledBackend();
      cleanup.backends.push(be.backendId);
      return be;
    });
  },

  makeBucket: async ({ cleanup }, use) => {
    await use(async (opts) => {
      const b = await seedBucket(opts);
      cleanup.buckets.push(b);
      return b;
    });
  },

  makeCollection: async ({ cleanup }, use) => {
    await use(async (opts) => {
      const c = await seedCollection(opts);
      cleanup.collections.push(c);
      return c;
    });
  },
});

export { expect } from "@playwright/test";
