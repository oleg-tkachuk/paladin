/**
 * The run left nothing behind.
 *
 * Runs after every test, as the environment project's teardown, and compares
 * the stack against the inventory the environment project took at the start
 * (fixtures/leaks.ts). A bucket, backend or active tenant that is new since
 * then is one a teardown failed to remove, and fails here by name: each
 * bucket holds SeaweedFS volume slots, and leaked ones are what filled the
 * store until it refused new buckets.
 *
 * So does a collection: a trashed object still holds one, and every run that
 * tested the trash left two behind with only a warning in the log.
 *
 * So does an OAuth grant the seeded admin still holds: one outlives its test
 * by weeks, and they piled up a row per grant per run.
 *
 * Tenants new in the trash fail it too. They used to be only reported, while
 * a tenant's charges held it there (charges.tenant_id was RESTRICT); the purge
 * now removes a tenant's billing rows with it, and a trash that took leftovers
 * quietly is where they accumulated.
 */
import { readFile } from "node:fs/promises";
import path from "node:path";

import { test, expect } from "@playwright/test";

import {
  LEAK_BASELINE_FILE,
  addedSince,
  fixtureInventory,
  type FixtureInventory,
} from "./fixtures/leaks";

/** The environment project's name, whose output holds the baseline. */
const ENVIRONMENT_PROJECT = "environment";

test("the run removed what it created", async ({}, testInfo) => {
  const environment = testInfo.config.projects.find(
    (p) => p.name === ENVIRONMENT_PROJECT,
  );
  expect(environment, "the environment project is configured").toBeDefined();
  const before = JSON.parse(
    await readFile(
      path.join(environment!.outputDir, LEAK_BASELINE_FILE),
      "utf8",
    ),
  ) as FixtureInventory;
  const added = addedSince(before, await fixtureInventory());

  expect(
    added,
    "resources this run created and its teardown did not remove",
  ).toEqual({
    activeTenants: [],
    trashedTenants: [],
    buckets: [],
    backends: [],
    collections: [],
    oauthGrants: [],
  });
});
