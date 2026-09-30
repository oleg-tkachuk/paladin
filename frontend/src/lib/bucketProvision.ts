// Bucket.provision_state, as admindomain.BucketProvisionState* spells it.
export const PROVISION_STATE = {
  pending: "pending",
  ready: "ready",
  failed: "failed",
  deleting: "deleting",
  deletionFailed: "deletion_failed",
} as const;

/** How often a list holding an in-flight bucket asks again. */
export const PROVISION_POLL_MS = 3_000;

/** A state the reconciler has yet to move on from. */
export function isProvisionInFlight(state: string): boolean {
  return (
    state === PROVISION_STATE.pending || state === PROVISION_STATE.deleting
  );
}
