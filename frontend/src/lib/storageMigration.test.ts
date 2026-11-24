import { describe, expect, it } from "vitest";
import {
  MIGRATION_CLEANED,
  MIGRATION_COMPLETED,
  MIGRATION_FAILED,
  MIGRATION_MOVING,
  MIGRATION_PROVISIONING,
  MIGRATION_ROLLING_BACK,
  canCancelMigration,
  migrationCancelWarning,
  migrationInFlight,
} from "./storageMigration";

describe("storageMigration", () => {
  it.each([
    [MIGRATION_PROVISIONING, true],
    [MIGRATION_MOVING, true],
    [MIGRATION_COMPLETED, true],
    [MIGRATION_ROLLING_BACK, false],
    [MIGRATION_FAILED, false],
    [MIGRATION_CLEANED, false],
  ])("cancel offered for %s: %s", (state, want) => {
    expect(canCancelMigration(state)).toBe(want);
  });

  it.each([
    [MIGRATION_PROVISIONING, true],
    [MIGRATION_MOVING, true],
    [MIGRATION_ROLLING_BACK, true],
    [MIGRATION_COMPLETED, false],
    [MIGRATION_FAILED, false],
    [MIGRATION_CLEANED, false],
  ])("polled in %s: %s", (state, want) => {
    expect(migrationInFlight(state)).toBe(want);
  });

  it("says a provisioning cancel moves nothing, and a later one moves objects back", () => {
    expect(migrationCancelWarning(MIGRATION_PROVISIONING)).toMatch(
      /fails at once/,
    );
    expect(migrationCancelWarning(MIGRATION_MOVING)).toMatch(
      /goes back to the shared bucket/,
    );
  });
});
