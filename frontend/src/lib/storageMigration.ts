// StorageMigrationStatus.state values (ADR-0015, ADR-0051), and what the
// console may do with a migration in each. Mirrors backend package migstate.

export const MIGRATION_PROVISIONING = "provisioning";
export const MIGRATION_MOVING = "moving";
export const MIGRATION_COMPLETED = "completed";
export const MIGRATION_CLEANED = "cleaned";
export const MIGRATION_ROLLING_BACK = "rolling_back";
export const MIGRATION_FAILED = "failed";

/**
 * States the console stops polling in: nothing changes there without an
 * operator. `completed` is one — what is left is the cleanup after its
 * retention, which the status shows only as `cleaned`.
 */
const SETTLED_STATES: ReadonlySet<string> = new Set([
  MIGRATION_COMPLETED,
  MIGRATION_CLEANED,
  MIGRATION_FAILED,
]);

/** Whether the console keeps polling a migration in state. */
export function migrationInFlight(state: string): boolean {
  return !SETTLED_STATES.has(state);
}

/**
 * States CancelTenantStorageMigration accepts (migstate.CancelTo): a
 * provisioning migration fails, a moving or completed one rolls back. The
 * server decides; this only offers the action where it can apply.
 */
const CANCELLABLE_STATES: ReadonlySet<string> = new Set([
  MIGRATION_PROVISIONING,
  MIGRATION_MOVING,
  MIGRATION_COMPLETED,
]);

/** Whether a migration in state can be cancelled. */
export function canCancelMigration(state: string): boolean {
  return CANCELLABLE_STATES.has(state);
}

/** What a cancel does to a migration in state, for its confirmation. */
export function migrationCancelWarning(state: string): string {
  return state === MIGRATION_PROVISIONING
    ? "The dedicated bucket is not used yet: the migration fails at once and the tenant stays on its shared bucket."
    : "Every object already moved goes back to the shared bucket, and new uploads go there again. The migration then fails, and can be started again.";
}
