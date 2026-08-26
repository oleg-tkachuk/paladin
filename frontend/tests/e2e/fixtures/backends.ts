/* eslint-disable react-hooks/rules-of-hooks --
 * Playwright fixtures take a callback named `use`; the React lint rule sees
 * the name and assumes a hook. There is no React here. */
import { test as base } from "@playwright/test";
import {
  deleteBackends,
  seedDisabledBackend,
  seedEnabledBackend,
  type SeededBackend,
} from "./seed";

/**
 * A `test` that cleans up the storage backends it created.
 *
 * Same lesson as makeTenant, learned again on a different table: the backend
 * specs seeded five per run and deleted none, reaching 160 — and the
 * collection dialog, which defaults to the first backend it is offered,
 * started defaulting to a leftover with no buckets. Its bucket picker then
 * stayed disabled and the create test failed for reasons that had nothing to
 * do with creating collections.
 */
export const test = base.extend<{
  makeDisabledBackend: (opts?: { idPrefix?: string }) => Promise<SeededBackend>;
  makeEnabledBackend: () => Promise<SeededBackend>;
}>({
  makeDisabledBackend: async ({}, use) => {
    const created: string[] = [];
    await use(async (opts) => {
      const be = await seedDisabledBackend(opts);
      created.push(be.backendId);
      return be;
    });
    // Best-effort: a teardown failure must not fail a test that passed.
    if (created.length) await deleteBackends(created).catch(() => {});
  },

  makeEnabledBackend: async ({}, use) => {
    const created: string[] = [];
    await use(async () => {
      const be = await seedEnabledBackend();
      created.push(be.backendId);
      return be;
    });
    if (created.length) await deleteBackends(created).catch(() => {});
  },
});

export { expect } from "@playwright/test";
